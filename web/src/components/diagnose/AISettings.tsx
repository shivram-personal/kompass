// The "AI Diagnosis" section of the Settings dialog — the canonical home for the
// agent / isolation / model / effort preferences. Context-aware (reads the shared
// DiagnoseContext state the investigation panel also uses); renders nothing when
// no supported agent CLI is installed.
import { useDiagnose } from "./DiagnoseContext";
import { AgentControls } from "./parts";

export function AISettingsSection() {
  const d = useDiagnose();
  if (!d.available || d.agents.length === 0) return null;
  return (
    <section className="mb-5 rounded-md border border-theme-border bg-theme-elevated/50 p-3">
      <h3 className="mb-1 text-sm font-medium text-theme-text-primary">
        AI Diagnosis
      </h3>
      <p className="mb-3 text-xs text-theme-text-tertiary">
        Investigations run on your own machine via your installed agent CLI — no
        Radar cloud, no API key. These preferences apply to new investigations.
      </p>
      <AgentControls
        agents={d.agents}
        selectedAgent={d.selectedAgent}
        onSelectAgent={d.setSelectedAgent}
        isolated={d.isolated}
        onSetIsolated={d.setIsolated}
        model={d.model}
        onSetModel={d.setModel}
        effort={d.effort}
        onSetEffort={d.setEffort}
      />
    </section>
  );
}
