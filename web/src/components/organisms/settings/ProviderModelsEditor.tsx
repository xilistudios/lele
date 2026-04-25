import { useState } from "react";
import { useTranslation } from "react-i18next";
import { RemoveButton } from "../../atoms/RemoveButton";
import { BooleanInput, NumberInput, SettingsField } from "../../molecules";
import { ModelSearchInput, isOpenAICompatible } from "./ModelSearchInput";

type ProviderModels = Record<
  string,
  import("../../../lib/types").ProviderModelConfig
>;

type Props = {
  name: string;
  models: ProviderModels;
  onChange: (v: ProviderModels) => void;
  providerType?: string;
};

export function ProviderModelsEditor({
  name,
  models,
  onChange,
  providerType,
}: Props) {
  const { t } = useTranslation();
  const [newModelName, setNewModelName] = useState("");
  const modelNames = Object.keys(models);

  const addModel = (key: string) => {
    const trimmed = key.trim();
    if (!trimmed) return;
    onChange({ ...models, [trimmed]: {} });
  };

  const addModelFromInput = () => {
    addModel(newModelName);
    setNewModelName("");
  };

  const removeModel = (key: string) => {
    const updated = { ...models };
    delete updated[key];
    onChange(updated);
  };

  const isCompat = isOpenAICompatible(providerType);
  return (
    <div className="space-y-3">
      {isCompat ? (
        <ModelSearchInput
          providerName={name}
          providerType={providerType}
          existingModels={modelNames}
          onAddModel={addModel}
        />
      ) : (
        <div className="flex gap-2">
          <input
            type="text"
            value={newModelName}
            onChange={(e) => setNewModelName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addModelFromInput();
              }
            }}
            placeholder={t("settings.modelNamePlaceholder")}
            className="w-full rounded border border-border bg-background-primary px-3 py-2 text-xs text-text-primary placeholder:text-text-tertiary focus:border-interaction-primary focus:outline-none focus:ring-2 focus:ring-interaction-primary focus:ring-offset-2 focus:ring-offset-background-primary disabled:opacity-40"
          />
          <button
            type="button"
            onClick={addModelFromInput}
            disabled={!newModelName.trim()}
            className="rounded bg-cta-primary px-3 py-2 text-xs text-text-on-accent transition-colors hover:bg-cta-hover disabled:opacity-40"
          >
            {t("common.add")}
          </button>
        </div>
      )}

      {modelNames.length === 0 && (
        <p className="text-xs text-text-tertiary">{t("settings.noModels")}</p>
      )}

      {modelNames.map((key) => {
        const m = models[key];
        return (
          <div
            key={key}
            className="rounded border border-border bg-background-secondary p-3"
          >
            <div className="mb-2 flex items-center justify-between">
              <span className="font-mono text-xs font-medium text-text-primary">
                {key}
              </span>
              <RemoveButton
                onClick={() => removeModel(key)}
                ariaLabel={t("common.remove")}
              />
            </div>
            <div className="grid grid-cols-2 gap-2">
              <SettingsField
                label={t("settings.fields.modelContextWindow")}
                path={`providers.${name}.models.${key}.context_window`}
              >
                <NumberInput
                  id={`providers.${name}.models.${key}.context_window`}
                  value={m.context_window || 0}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: { ...m, context_window: v || undefined },
                    })
                  }
                  min={0}
                />
              </SettingsField>
              <SettingsField
                label={t("settings.fields.modelMaxTokens")}
                path={`providers.${name}.models.${key}.max_tokens`}
              >
                <NumberInput
                  id={`providers.${name}.models.${key}.max_tokens`}
                  value={m.max_tokens || 0}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: { ...m, max_tokens: v || undefined },
                    })
                  }
                  min={0}
                />
              </SettingsField>
              <SettingsField
                label={t("settings.fields.modelTemperature")}
                path={`providers.${name}.models.${key}.temperature`}
              >
                <NumberInput
                  id={`providers.${name}.models.${key}.temperature`}
                  value={m.temperature || 0}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: { ...m, temperature: v || undefined },
                    })
                  }
                  min={0}
                  max={2}
                  step={0.1}
                />
              </SettingsField>
              <SettingsField
                label={t("settings.fields.modelVision")}
                path={`providers.${name}.models.${key}.vision`}
              >
                <BooleanInput
                  id={`providers.${name}.models.${key}.vision`}
                  value={m.vision || false}
                  onChange={(v) =>
                    onChange({ ...models, [key]: { ...m, vision: v } })
                  }
                />
              </SettingsField>
              <SettingsField
                label={t("settings.fields.modelThinking")}
                path={`providers.${name}.models.${key}.reasoning.enable`}
              >
                <BooleanInput
                  id={`providers.${name}.models.${key}.reasoning`}
                  value={m.reasoning?.enable || false}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: { ...m, reasoning: { ...m.reasoning, enable: v } },
                    })
                  }
                />
              </SettingsField>
            </div>
          </div>
        );
      })}
    </div>
  );
}
