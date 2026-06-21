import { Sparkles } from "lucide-react";
import { useDiagnose } from "./DiagnoseContext";
import { Tooltip } from "../ui/Tooltip";
import type { RenderDiagnoseAction } from "../../context/DiagnoseCustomization";

// The per-resource "Diagnose with AI" button. It no longer owns a panel — it
// just dispatches to the single app-level AI surface (DiagnoseContext), opening
// it into a new investigation for this resource. Self-hides when no agent CLI is
// present. A host like Radar Hub overrides this slot with its own action.
function DiagnoseResourceButton({
  kind,
  namespace,
  name,
}: {
  kind: string;
  namespace: string;
  name: string;
}) {
  const d = useDiagnose();
  if (!d.available) return null;
  return (
    <Tooltip
      content={`Runs ${d.agentLabel} on your machine and sends it this resource's context (spec, events, logs) to find the root cause`}
      position="bottom"
    >
      <button
        onClick={() => d.openInvestigation({ kind, namespace, name })}
        className="inline-flex items-center gap-1.5 rounded-lg border border-theme-border px-2.5 py-1.5 text-sm text-theme-text-secondary hover:bg-theme-hover hover:text-theme-text-primary"
      >
        <Sparkles className="h-3.5 w-3.5 text-accent" />
        Diagnose
      </button>
    </Tooltip>
  );
}

export const defaultDiagnoseAction: RenderDiagnoseAction = ({
  kind,
  namespace,
  name,
}) => <DiagnoseResourceButton kind={kind} namespace={namespace} name={name} />;

// Compact per-issue "Diagnose" action for the Issues queue — launches an
// investigation for the issue's subject from where the problem is surfaced.
// stopPropagation so it doesn't toggle the issue row it lives in.
export function IssueDiagnoseButton({
  kind,
  namespace,
  name,
}: {
  kind: string;
  namespace: string;
  name: string;
}) {
  const d = useDiagnose();
  if (!d.available) return null;
  return (
    <Tooltip
      content={`Runs ${d.agentLabel} on your machine and sends it this resource's context to find the root cause`}
      position="left"
    >
      <button
        onClick={(e) => {
          e.stopPropagation();
          d.openInvestigation({ kind, namespace, name });
        }}
        className="flex shrink-0 items-center gap-1 rounded-md border border-theme-border px-2 py-1 text-xs text-theme-text-secondary hover:bg-theme-hover hover:text-theme-text-primary"
      >
        <Sparkles className="h-3 w-3 text-accent" />
        Diagnose
      </button>
    </Tooltip>
  );
}

// Global top-bar entry into the AI surface (opens its Home / recent
// investigations). Self-hides when no agent CLI is present.
export function GlobalDiagnoseButton() {
  const d = useDiagnose();
  if (!d.available) return null;
  return (
    <Tooltip content="AI investigations" position="bottom">
      <button
        onClick={d.openHome}
        className="rounded-md bg-theme-elevated p-1.5 text-theme-text-secondary transition-colors hover:bg-theme-hover hover:text-theme-text-primary"
        aria-label="AI investigations"
      >
        <Sparkles className="h-4 w-4 text-accent" />
      </button>
    </Tooltip>
  );
}
