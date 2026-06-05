import { describe, it, expect } from 'vitest'
import { neighborhoodFor } from './topology-neighborhood'
import type { Topology, NodeKind, EdgeType } from '../types/core'

function node(id: string, kind: string, ns: string, name: string): Topology['nodes'][number] {
  return { id, kind: kind as NodeKind, name, status: 'healthy' as Topology['nodes'][number]['status'], data: { namespace: ns } }
}
function edge(source: string, target: string, type: EdgeType): Topology['edges'][number] {
  return { id: `${source}->${target}`, source, target, type }
}

describe('neighborhoodFor', () => {
  // Deployment → ReplicaSet → Pod (manages), plus a Service exposing it and a
  // ConfigMap configuring it. All of it is the workload's neighborhood.
  it('includes the ownership chain + attached context', () => {
    const topo: Topology = {
      nodes: [
        node('dep', 'Deployment', 'app', 'web'),
        node('rs', 'ReplicaSet', 'app', 'web-abc'),
        node('pod', 'Pod', 'app', 'web-abc-1'),
        node('svc', 'Service', 'app', 'web'),
        node('cm', 'ConfigMap', 'app', 'web-config'),
      ],
      edges: [
        edge('dep', 'rs', 'manages'),
        edge('rs', 'pod', 'manages'),
        edge('svc', 'dep', 'exposes'),
        edge('cm', 'dep', 'configures'),
      ],
    }
    const out = neighborhoodFor(topo, [{ kind: 'Deployment', namespace: 'app', name: 'web' }])
    expect(new Set(out.nodes.map((n) => n.id))).toEqual(new Set(['dep', 'rs', 'pod', 'svc', 'cm']))
  })

  // The leaf rule: a ConfigMap shared by two unrelated Deployments must NOT
  // bridge the second Deployment into the first's neighborhood.
  it('does not bleed through a shared ConfigMap', () => {
    const topo: Topology = {
      nodes: [
        node('depA', 'Deployment', 'app', 'a'),
        node('depB', 'Deployment', 'app', 'b'),
        node('cm', 'ConfigMap', 'app', 'shared'),
      ],
      edges: [
        edge('cm', 'depA', 'configures'),
        edge('cm', 'depB', 'configures'),
      ],
    }
    const out = neighborhoodFor(topo, [{ kind: 'Deployment', namespace: 'app', name: 'a' }])
    const ids = new Set(out.nodes.map((n) => n.id))
    expect(ids.has('depA')).toBe(true)
    expect(ids.has('cm')).toBe(true) // the shared ConfigMap IS shown (context)
    expect(ids.has('depB')).toBe(false) // …but it doesn't drag in the other app
  })

  // A GitOps manager reached upward is a leaf: "managed by" is shown, but its
  // sibling workloads are not pulled in.
  it('does not expand through a GitOps manager to its siblings', () => {
    const topo: Topology = {
      nodes: [
        node('ks', 'Kustomization', 'flux-system', 'apps'),
        node('depA', 'Deployment', 'app', 'a'),
        node('depB', 'Deployment', 'app', 'b'),
      ],
      edges: [
        edge('ks', 'depA', 'manages'),
        edge('ks', 'depB', 'manages'),
      ],
    }
    const out = neighborhoodFor(topo, [{ kind: 'Deployment', namespace: 'app', name: 'a' }])
    const ids = new Set(out.nodes.map((n) => n.id))
    expect(ids.has('depA')).toBe(true)
    expect(ids.has('ks')).toBe(true) // the managing Kustomization is shown
    expect(ids.has('depB')).toBe(false) // …but not the Kustomization's other app
  })

  it('returns an empty graph with a warning when no seed matches', () => {
    const topo: Topology = { nodes: [node('dep', 'Deployment', 'app', 'web')], edges: [] }
    const out = neighborhoodFor(topo, [{ kind: 'Deployment', namespace: 'app', name: 'missing' }])
    expect(out.nodes).toHaveLength(0)
    expect(out.warnings?.some((w) => w.includes('No topology nodes matched'))).toBe(true)
  })
})
