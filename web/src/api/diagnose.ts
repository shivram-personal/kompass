// Client for the local AI-diagnose engine (OSS BYO-agent). The agent CLI runs
// on the user's own machine/subscription against Radar's MCP; this just starts
// the investigation and consumes its SSE event stream.
import { getApiBase, getCredentialsMode } from "./config";

export interface AgentInfo {
  name: string;
  label: string;
  path: string;
  version: string;
  present: boolean;
  supported: boolean;
}

export interface AgentsResponse {
  agents: AgentInfo[];
  enabled: boolean;
}

export interface DiagnoseStep {
  id: string;
  tool: string;
  status: "running" | "done";
  ms?: number;
  summary?: string;
}

export interface Diagnosis {
  rootCause: string;
  report: string;
  remediation: string[];
  confidence?: number;
  costUsd?: number;
  turns?: number;
}

export interface DiagnoseStreamEvent {
  type: "phase" | "step" | "token" | "done" | "error";
  phase?: string;
  step?: DiagnoseStep;
  token?: string;
  diagnosis?: Diagnosis;
  error?: string;
}

export async function fetchAgents(
  signal?: AbortSignal,
): Promise<AgentsResponse> {
  const res = await fetch(`${getApiBase()}/agents`, {
    credentials: getCredentialsMode(),
    signal,
  });
  if (!res.ok) throw new Error(`agents: ${res.status}`);
  return res.json();
}

export interface DiagnoseHandlers {
  onEvent: (ev: DiagnoseStreamEvent) => void;
  onClose?: () => void;
}

/**
 * Starts a streaming investigation. Returns a cancel function that closes the
 * SSE connection (which aborts the request — the server kills the agent's
 * process group on disconnect).
 */
export function streamDiagnose(
  params: { kind: string; namespace: string; name: string },
  handlers: DiagnoseHandlers,
): () => void {
  const q = new URLSearchParams({
    kind: params.kind,
    namespace: params.namespace,
    name: params.name,
  });
  const url = `${getApiBase()}/diagnose/stream?${q.toString()}`;
  const es = new EventSource(url, {
    withCredentials: getCredentialsMode() === "include",
  });

  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    es.close();
    handlers.onClose?.();
  };

  const dispatch = (type: DiagnoseStreamEvent["type"]) => (e: MessageEvent) => {
    let ev: DiagnoseStreamEvent;
    try {
      ev = JSON.parse(e.data);
    } catch {
      return;
    }
    handlers.onEvent(ev);
    if (type === "done" || type === "error") close();
  };

  for (const t of ["phase", "step", "token", "done", "error"] as const) {
    es.addEventListener(t, dispatch(t));
  }
  // A transport error after we've started: surface once, then stop (EventSource
  // would otherwise auto-reconnect and restart the whole investigation).
  es.onerror = () => {
    if (closed) return;
    handlers.onEvent({
      type: "error",
      error: "Connection to the investigation stream was lost.",
    });
    close();
  };

  return close;
}
