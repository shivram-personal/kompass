// Mirrors internal/trace types.go on the Go side. Keep field names in sync
// when extending — JSON tags on Go must match these property names exactly.

export type Verdict = 'healthy' | 'degraded' | 'broken' | 'unknown'
export type FindingSeverity = 'critical' | 'warning' | 'info'

export interface ResourceRef {
  group?: string
  kind: string
  namespace?: string
  name: string
}

export interface Finding {
  code: string
  severity: FindingSeverity
  message: string
  /** Parsed root cause (plain English) when the detector classified the
   *  failure — e.g. "Image not found in registry", "Exit code 137 (OOMKilled)".
   *  Empty for generic detections; UI renders message in that case. */
  cause?: string
  /** Next-step guidance paired with cause — e.g. "Push the image or fix the
   *  reference", "Increase the container memory limit". Empty when not parsed. */
  action?: string
  remediation?: string
  command?: string
}

export interface Hop {
  resource: ResourceRef
  edge: string
  findings: Finding[]
  meta?: Record<string, unknown>
  config?: HopConfig
  probes?: ProbeResult[]
}

export type ProbeLayer = 'dns' | 'tcp' | 'tls' | 'http'
export type ProbeVantage = 'in-cluster' | 'local'
/**
 * Which route a Service/Pod probe took. "data" = straight to the resource
 * over the network (kube-proxy for ClusterIP, direct dial for PodIP).
 * "apiserver" = through the K8s API server's proxy subresource. Empty when
 * the question doesn't apply (DNS, HTTP to an Ingress hostname, etc.).
 */
export type ProbePath = 'data' | 'apiserver'

/**
 * Tone classifies a Result for the UI when ok alone is too coarse. HTTP uses
 * it to distinguish 2xx (healthy) from 3xx/4xx (degraded — responded but not
 * at the expected route, or redirected without follow) and 5xx (unhealthy).
 * Empty falls back to (skipped → unknown, !ok → unhealthy, ok → healthy).
 */
export type ProbeTone = 'healthy' | 'degraded' | 'unhealthy'

export interface ProbeResult {
  layer: ProbeLayer
  target: string
  vantage: ProbeVantage
  path?: ProbePath
  ok: boolean
  tone?: ProbeTone
  skipped?: boolean
  reason?: string
  latencyNs?: number
  detail?: string
  error?: string
}

export interface PortMap {
  name?: string
  port: number
  targetPort?: string
  protocol?: string
  appProtocol?: string
}

export interface ContainerPortRef {
  container: string
  name?: string
  port: number
  protocol?: string
}

export interface ProbeRef {
  container: string
  type: string
  port?: string
  path?: string
  scheme?: string
}

export interface BackendRef {
  kind: string
  name: string
  namespace?: string
  port?: string
}

export interface RouteRule {
  hosts?: string[]
  paths?: string[]
  backends?: BackendRef[]
}

export interface GatewayListener {
  name?: string
  port: number
  protocol?: string
  hostname?: string
}

export interface HopConfig {
  ports?: PortMap[]
  serviceType?: string
  clusterIP?: string
  selector?: Record<string, string>
  containerPorts?: ContainerPortRef[]
  probes?: ProbeRef[]
  hostnames?: string[]
  rules?: RouteRule[]
  listeners?: GatewayListener[]
  addresses?: string[]
  podIPs?: string[]
  podNames?: string[]
}

export interface Trace {
  subject: ResourceRef
  upstreams: Hop[]
  downstream: Hop[]
  verdict: Verdict
  brokenAt: number
  reason?: string
  truncated?: boolean
}
