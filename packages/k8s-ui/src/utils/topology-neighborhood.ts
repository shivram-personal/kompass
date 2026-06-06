import type { Topology, TopologyNode, TopologyEdge, EdgeType, NodeKind } from '../types/core'

// Seeded neighborhood query — the shared primitive behind the WorkloadView
// Topology tab (seed = one workload) and the Application topology (seed = the
// app's workloads). Given a full topology and a seed set, it returns the
// subgraph "everything relevant to these seeds": the seeds' ownership cores plus
// their attached context (Services, config, autoscalers, policies) — without
// letting a shared resource bridge in unrelated workloads.
//
// The traversal is the load-bearing part. Edges fall into three classes:
//
//   identity (`manages`)            — the ownerRef / controller chain
//                                     (Deployment→ReplicaSet→Pod). Walk it: a
//                                     workload's pods ARE the workload.
//   routing  (`exposes`,`routes-to`)— a Service/Ingress/Route in front of the
//                                     workload. INCLUDE it, but as a LEAF: a
//                                     shared Ingress fronts many unrelated apps,
//                                     so we don't expand THROUGH it.
//   context  (`configures`,`uses`,  — a ConfigMap/Secret/HPA/PDB attached to the
//             `protects`)             workload. INCLUDE as a LEAF: a shared
//                                     ConfigMap mounted by two apps must not glue
//                                     them into one neighborhood.
//
// Two more rules keep it honest:
//   - GitOps managers reached UPWARD (Argo Application / Flux Kustomization /
//     HelmRelease / GitRepository) are LEAVES — we show "managed by X" but never
//     expand down to X's OTHER children (the same over-merge the app resolver's
//     structuralRoot fix prevents, in graph form).
//   - Degree guard: any node whose fan-out along an edge type exceeds K is
//     treated as a leaf regardless of class — the graph itself flags "this is
//     shared infrastructure," no labels needed.
//
// The result is the raw subgraph; the caller hands it to <TopologyGraph/>.

export interface NeighborhoodSeed {
  kind: string
  namespace: string
  name: string
}

export interface NeighborhoodOptions {
  /** Fan-out above which a node along one edge type is treated as shared infra
   *  (a leaf) even if its edge class would otherwise traverse. */
  degreeGuard?: number
}

const IDENTITY_EDGES = new Set<EdgeType>(['manages'])
const ROUTING_EDGES = new Set<EdgeType>(['exposes', 'routes-to'])
// context: 'configures' | 'uses' | 'protects' — everything else is leaf-attached.

// GitOps managers: included as context ("managed by"), never expanded through.
const GITOPS_MANAGER_KINDS = new Set<NodeKind>([
  'Application',
  'Kustomization',
  'HelmRelease',
  'GitRepository',
] as NodeKind[])

const DEFAULT_DEGREE_GUARD = 8

function nodeNamespace(node: TopologyNode): string {
  const ns = node.data?.namespace
  return typeof ns === 'string' ? ns : ''
}

/** Filter a topology to the neighborhood of `seeds`. Returns the subgraph; an
 *  empty graph (with a warning) when no seed node matches. */
export function neighborhoodFor(
  topology: Topology,
  seeds: NeighborhoodSeed[],
  opts: NeighborhoodOptions = {},
): Topology {
  const K = opts.degreeGuard ?? DEFAULT_DEGREE_GUARD

  const nodeById = new Map<string, TopologyNode>()
  for (const n of topology.nodes) nodeById.set(n.id, n)

  const seedIds = new Set<string>()
  for (const n of topology.nodes) {
    if (seeds.some((s) => s.kind === n.kind && s.name === n.name && s.namespace === nodeNamespace(n))) {
      seedIds.add(n.id)
    }
  }
  if (seedIds.size === 0) {
    return {
      ...topology,
      nodes: [],
      edges: [],
      warnings: [...(topology.warnings ?? []), 'No topology nodes matched this selection.'],
    }
  }

  // adjacency (both directions) + per-node fan-out by edge type for the guard.
  const adjacency = new Map<string, TopologyEdge[]>()
  const fanout = new Map<string, Map<EdgeType, number>>()
  for (const e of topology.edges) {
    for (const id of [e.source, e.target]) {
      if (!adjacency.has(id)) adjacency.set(id, [])
      adjacency.get(id)!.push(e)
      if (!fanout.has(id)) fanout.set(id, new Map())
      const m = fanout.get(id)!
      m.set(e.type, (m.get(e.type) ?? 0) + 1)
    }
  }
  const highFanout = (id: string, type: EdgeType): boolean => (fanout.get(id)?.get(type) ?? 0) > K

  const keep = new Set(seedIds)
  // Nodes included for context but never expanded THROUGH.
  const leaf = new Set<string>()
  const queue: string[] = Array.from(seedIds)

  while (queue.length) {
    const id = queue.shift()!
    if (leaf.has(id)) continue // a leaf is a dead end — don't traverse out of it
    for (const e of adjacency.get(id) ?? []) {
      const nextId = e.source === id ? e.target : e.source
      const nextNode = nodeById.get(nextId)
      if (!nextNode) continue

      let asLeaf: boolean
      if (IDENTITY_EDGES.has(e.type)) {
        // ownerRef chain — traverse, unless `next` is a GitOps manager reached
        // upward: include it as "managed by", but don't fan out to its siblings.
        asLeaf = GITOPS_MANAGER_KINDS.has(nextNode.kind)
      } else if (ROUTING_EDGES.has(e.type)) {
        asLeaf = true // a Service/Ingress in front of the workload — leaf
      } else {
        asLeaf = true // configures / uses / protects — leaf
      }
      // The graph's own signal: a high-fan-out node is shared infra → leaf.
      if (highFanout(nextId, e.type)) asLeaf = true

      if (!keep.has(nextId)) {
        keep.add(nextId)
        if (asLeaf) leaf.add(nextId)
        queue.push(nextId)
      }
    }
  }

  return {
    ...topology,
    nodes: topology.nodes.filter((n) => keep.has(n.id)),
    edges: topology.edges.filter((e) => keep.has(e.source) && keep.has(e.target)),
  }
}

// ─── Workload ownership tagging ──────────────────────────────────────────────
//
// For the application graph (seeds = the app's workloads) we want to show which
// resources belong to which workload. A resource is "owned" by a workload when
// it belongs to that workload ALONE — its pods (manages-descendants), and the
// Service/config/policy attached to exactly one workload. Anything attached to
// two or more workloads (a shared ConfigMap, a GitOps manager) stays NEUTRAL, as
// does anything attached to none. This is the visual twin of the leaf rule: the
// graph already refuses to bridge through shared resources, and here they
// refuse to claim a color.

export interface WorkloadOwnership {
  /** The neighborhood subgraph, each node's `data` stamped with:
   *   - `ownerWorkloadId` (workload key | null) + `ownerColorIndex` (number | null)
   *     — the EXCLUSIVE owner, for the color wash. Shared nodes are null (neutral).
   *   - `focusWorkloadIds` (workload keys) — every workload whose neighborhood
   *     includes this node, for hover-focus. A shared ConfigMap belongs to all
   *     workloads that use it, so focusing any of them lights it up (matching the
   *     single-workload topology), even though it stays neutral-colored. */
  topology: Topology
  /** Color index per workload key (`kind/namespace/name`) — for rail swatches. */
  colorByWorkload: Map<string, number>
}

const workloadKeyOf = (kind: string, namespace: string, name: string): string =>
  `${kind}/${namespace}/${name}`

/** Run the neighborhood query for `seeds`, then tag each node with the workload
 *  that exclusively owns it (or neutral). Returns the tagged subgraph plus the
 *  color + node-ownership maps the application rail needs. */
export function tagWorkloadOwnership(topology: Topology, seeds: NeighborhoodSeed[]): WorkloadOwnership {
  const sub = neighborhoodFor(topology, seeds)

  // Stable color per workload: order of `seeds` (matches the rail's order).
  const colorByWorkload = new Map<string, number>()
  for (const s of seeds) {
    const k = workloadKeyOf(s.kind, s.namespace, s.name)
    if (!colorByWorkload.has(k)) colorByWorkload.set(k, colorByWorkload.size)
  }

  // The seed nodes present in the subgraph, by their workload key.
  const seedKeyById = new Map<string, string>()
  for (const n of sub.nodes) {
    if (seeds.some((s) => s.kind === n.kind && s.name === n.name && s.namespace === nodeNamespace(n))) {
      seedKeyById.set(n.id, workloadKeyOf(n.kind, nodeNamespace(n), n.name))
    }
  }

  // manages-DOWN children (source manages target) + undirected neighbors.
  const downChildren = new Map<string, string[]>()
  const neighbors = new Map<string, Set<string>>()
  for (const e of sub.edges) {
    if (e.type === 'manages') {
      if (!downChildren.has(e.source)) downChildren.set(e.source, [])
      downChildren.get(e.source)!.push(e.target)
    }
    for (const [a, b] of [[e.source, e.target], [e.target, e.source]] as const) {
      if (!neighbors.has(a)) neighbors.set(a, new Set())
      neighbors.get(a)!.add(b)
    }
  }

  // Core = each seed plus everything reachable DOWN the manages chain from it
  // (its ReplicaSets, Pods). Exclusive by construction — a pod has one controller.
  const coreOwner = new Map<string, string>()
  for (const [seedId, key] of seedKeyById) {
    const queue = [seedId]
    while (queue.length) {
      const id = queue.shift()!
      if (coreOwner.has(id)) continue
      coreOwner.set(id, key)
      for (const c of downChildren.get(id) ?? []) if (!coreOwner.has(c)) queue.push(c)
    }
  }

  // For each node, figure out which workloads it belongs to. A core node (the
  // workload itself + its manages-descendants) belongs to its own workload. Any
  // other node belongs to every workload-core it touches: that's its focus set.
  // The color owner is the EXCLUSIVE case only — a node touching exactly one
  // workload and not a GitOps manager (managers are context, never owned).
  const nodes = sub.nodes.map((n) => {
    const core = coreOwner.get(n.id) ?? null
    let focusWorkloadIds: string[]
    let owner: string | null
    if (core) {
      focusWorkloadIds = [core]
      owner = core
    } else {
      const related = new Set<string>()
      for (const nb of neighbors.get(n.id) ?? []) {
        const o = coreOwner.get(nb)
        if (o) related.add(o)
      }
      focusWorkloadIds = [...related]
      owner = related.size === 1 && !GITOPS_MANAGER_KINDS.has(n.kind) ? [...related][0] : null
    }
    return {
      ...n,
      data: {
        ...n.data,
        ownerWorkloadId: owner,
        ownerColorIndex: owner ? colorByWorkload.get(owner) ?? null : null,
        focusWorkloadIds,
      },
    }
  })

  return { topology: { ...sub, nodes }, colorByWorkload }
}

/** The set of node IDs that are the seeds themselves — handy for the caller to
 *  pass `focusNodeId` (pan/zoom to the workload) into <TopologyGraph/>. */
export function seedNodeIds(topology: Topology, seeds: NeighborhoodSeed[]): string[] {
  const ids: string[] = []
  for (const n of topology.nodes) {
    if (seeds.some((s) => s.kind === n.kind && s.name === n.name && s.namespace === nodeNamespace(n))) {
      ids.push(n.id)
    }
  }
  return ids
}
