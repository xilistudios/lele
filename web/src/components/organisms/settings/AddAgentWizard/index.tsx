import { useState } from "react";
import { useSettings } from "../../../../contexts/SettingsContext";
import type { EditableAgentConfig } from "../../../../lib/types";
import { Modal } from "../../../atoms";
import { AgentPreview } from "./AgentPreview";
import { BasicInfoStep } from "./BasicInfoStep";
import { BehaviorStep } from "./BehaviorStep";
import { ModelStep } from "./ModelStep";
import { SkillsStep } from "./SkillsStep";
import { StepIndicator } from "./StepIndicator";

type Props = {
  isOpen: boolean;
  onClose: () => void;
};

const STEPS = [
  {
    id: 1,
    title: "settings.addAgentModal.stepBasicInfoTitle",
    icon: (
      <svg
        width="18"
        height="18"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
      >
        <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" />
        <circle cx="12" cy="7" r="4" />
      </svg>
    ),
  },
  {
    id: 2,
    title: "settings.addAgentModal.stepModelTitle",
    icon: (
      <svg
        width="18"
        height="18"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
      >
        <rect x="2" y="3" width="20" height="14" rx="2" ry="2" />
        <line x1="8" y1="21" x2="16" y2="21" />
        <line x1="12" y1="17" x2="12" y2="21" />
      </svg>
    ),
  },
  {
    id: 3,
    title: "settings.addAgentModal.stepBehaviorTitle",
    icon: (
      <svg
        width="18"
        height="18"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
      >
        <circle cx="12" cy="12" r="3" />
        <path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
      </svg>
    ),
  },
  {
    id: 4,
    title: "settings.addAgentModal.stepSkillsTitle",
    icon: (
      <svg
        width="18"
        height="18"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
      >
        <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />
      </svg>
    ),
  },
];

export function AddAgentModal({ isOpen, onClose }: Props) {
  const { draftConfig, updateField, t } = useSettings();

  const [step, setStep] = useState(1);
  const [isSubmitting, setIsSubmitting] = useState(false);

  // Form state
  const [agentId, setAgentId] = useState("");
  const [agentName, setAgentName] = useState("");
  const [isDefault, setIsDefault] = useState(false);
  const [primaryModel, setPrimaryModel] = useState("");
  const [fallbacks, setFallbacks] = useState<string[]>([]);
  const [temperature, setTemperature] = useState(0.7);
  const [maxIterations, setMaxIterations] = useState(10);
  const [maxTokens, setMaxTokens] = useState(4096);
  const [contextWindow, setContextWindow] = useState(128000);
  const [enableThinking, setEnableThinking] = useState(false);
  const [supportsImages, setSupportsImages] = useState(false);
  const [skills, setSkills] = useState<string[]>([]);

  const resetForm = () => {
    setStep(1);
    setIsSubmitting(false);
    setAgentId("");
    setAgentName("");
    setIsDefault(false);
    setPrimaryModel("");
    setFallbacks([]);
    setTemperature(0.7);
    setMaxIterations(10);
    setMaxTokens(4096);
    setContextWindow(128000);
    setEnableThinking(false);
    setSupportsImages(false);
    setSkills([]);
  };

  const handleClose = () => {
    resetForm();
    onClose();
  };

  const handleAdd = async () => {
    if (!draftConfig || !agentId.trim() || isDuplicate) return;

    setIsSubmitting(true);

    const list = draftConfig.agents.list || [];

    const newAgent: EditableAgentConfig = {
      id: agentId.trim(),
      default: isDefault || undefined,
      name: agentName.trim() || undefined,
      workspace: `~/.lele/workspace-${agentId.trim()}`,
      model: primaryModel
        ? {
            primary: primaryModel,
            fallbacks: fallbacks.length > 0 ? fallbacks : undefined,
          }
        : fallbacks.length > 0
        ? { fallbacks }
        : undefined,
      temperature: temperature !== 0.7 ? temperature : undefined,
      skills: skills.length > 0 ? skills : undefined,
      reasoning: enableThinking ? { enable: true } : undefined,
      max_iterations: maxIterations !== 10 ? maxIterations : undefined,
      max_tokens: maxTokens !== 4096 ? maxTokens : undefined,
      context_window: contextWindow !== 128000 ? contextWindow : undefined,
      supports_images: supportsImages || undefined,
    };

    updateField("agents.list", [...list, newAgent]);

    // Small delay for visual feedback
    await new Promise((resolve) => setTimeout(resolve, 300));

    handleClose();
  };

  const canProceedStep1 = agentId.trim() !== "";
  const isDuplicate = (draftConfig?.agents.list || []).some(
    (a) => a.id === agentId.trim(),
  );
  const canAdd = canProceedStep1 && !isDuplicate;

  const handleStepClick = (targetStep: number) => {
    // Only allow going back or to next available step
    if (targetStep <= step || (targetStep === step + 1 && canProceedStep1)) {
      setStep(targetStep);
    }
  };

  const handleNext = () => {
    if (step < STEPS.length) {
      setStep(step + 1);
    }
  };

  const handleBack = () => {
    if (step > 1) {
      setStep(step - 1);
    }
  };

  const tipKey = [
    "settings.addAgentModal.tipIdentity",
    "settings.addAgentModal.tipModel",
    "settings.addAgentModal.tipBehavior",
    "settings.addAgentModal.tipSkills",
  ][step - 1];

  return (
    <Modal
      isOpen={isOpen}
      onClose={handleClose}
      title={t("settings.addAgentModal.title")}
      size="md"
    >
      <div className="flex gap-6 p-6">
        {/* Left side: Step content */}
        <div className="flex-1 min-w-0">
          {/* Step indicators */}
          <div className="mb-6">
            <StepIndicator
              steps={STEPS.map((s) => ({ ...s, title: t(s.title) }))}
              currentStep={step}
              onStepClick={handleStepClick}
            />
          </div>

          {/* Step content with animation */}
          <div className="relative min-h-[300px]">
            <div className="transition-all duration-300 ease-in-out">
              {step === 1 && (
                <BasicInfoStep
                  agentId={agentId}
                  setAgentId={setAgentId}
                  agentName={agentName}
                  setAgentName={setAgentName}
                  isDefault={isDefault}
                  setIsDefault={setIsDefault}
                  isValid={canProceedStep1}
                  isDuplicate={isDuplicate}
                />
              )}
              {step === 2 && (
                <ModelStep
                  primaryModel={primaryModel}
                  setPrimaryModel={setPrimaryModel}
                  fallbacks={fallbacks}
                  setFallbacks={setFallbacks}
                />
              )}
              {step === 3 && (
                <BehaviorStep
                  temperature={temperature}
                  setTemperature={setTemperature}
                  maxIterations={maxIterations}
                  setMaxIterations={setMaxIterations}
                  maxTokens={maxTokens}
                  setMaxTokens={setMaxTokens}
                  contextWindow={contextWindow}
                  setContextWindow={setContextWindow}
                  enableThinking={enableThinking}
                  setEnableThinking={setEnableThinking}
                  supportsImages={supportsImages}
                  setSupportsImages={setSupportsImages}
                />
              )}
              {step === 4 && (
                <SkillsStep skills={skills} setSkills={setSkills} />
              )}
            </div>
          </div>

          {/* Navigation */}
          <div className="mt-6 flex justify-between pt-4 border-t border-border">
            <button
              type="button"
              onClick={handleBack}
              disabled={step === 1}
              className="flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium transition-all duration-200
                bg-background-secondary text-text-secondary hover:bg-background-tertiary hover:text-text-primary
                disabled:opacity-40 disabled:cursor-not-allowed"
            >
              <svg
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <polyline points="15 18 9 12 15 6" />
              </svg>
              {t("settings.addAgentModal.back")}
            </button>

            {step < STEPS.length ? (
              <button
                type="button"
                onClick={handleNext}
                disabled={step === 1 && !canProceedStep1}
                className="flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium transition-all duration-200
                  bg-blue-600 text-white hover:bg-blue-500
                  disabled:opacity-40 disabled:cursor-not-allowed"
              >
                {t("settings.addAgentModal.next")}
                <svg
                  width="16"
                  height="16"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                >
                  <polyline points="9 18 15 12 9 6" />
                </svg>
              </button>
            ) : (
              <button
                type="button"
                onClick={handleAdd}
                disabled={!canAdd || isSubmitting}
                className="flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium transition-all duration-200
                  bg-blue-600 text-white hover:bg-blue-500
                  disabled:opacity-40 disabled:cursor-not-allowed"
              >
                {isSubmitting ? (
                  <>
                    <svg
                      className="animate-spin h-4 w-4"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2"
                    >
                      <circle
                        cx="12"
                        cy="12"
                        r="10"
                        strokeDasharray="60"
                        strokeDashoffset="10"
                      />
                    </svg>
                    {t("settings.addAgentModal.creating")}
                  </>
                ) : (
                  <>
                    <svg
                      width="16"
                      height="16"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2"
                    >
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                    {t("settings.addAgentModal.add")}
                  </>
                )}
              </button>
            )}
          </div>
        </div>

        {/* Right side: Live preview */}
        <div className="w-56 shrink-0 border-l border-border pl-6 hidden lg:block">
          <div className="sticky top-0">
            <p className="text-xs font-medium text-text-tertiary uppercase tracking-wider mb-4">
              {t("settings.addAgentModal.preview")}
            </p>
            <AgentPreview
              agentId={agentId}
              agentName={agentName}
              isDefault={isDefault}
              primaryModel={primaryModel}
              fallbacks={fallbacks}
              temperature={temperature}
              skills={skills}
              enableThinking={enableThinking}
              supportsImages={supportsImages}
            />

            {/* Quick tips */}
            <div className="mt-6 space-y-3">
              <p className="text-xs font-medium text-text-tertiary uppercase tracking-wider">
                {t("settings.addAgentModal.tips")}
              </p>
              <p className="text-xs text-text-tertiary leading-relaxed">
                {t(tipKey)}
              </p>
            </div>
          </div>
        </div>
      </div>
    </Modal>
  );
}
