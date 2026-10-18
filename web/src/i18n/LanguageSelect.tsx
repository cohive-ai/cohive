import { useTranslation } from "react-i18next";
import i18n, { languages, setLanguage } from ".";

// 语言选择：每种语言用它自己的文字显示名称
export function LanguageSelect({ className = "" }: { className?: string }) {
  const { t, i18n: current } = useTranslation();
  return (
    <select
      aria-label={t("language.label")}
      className={`rounded border border-line bg-surface px-2 py-1 text-sm text-ink ${className}`}
      value={current.resolvedLanguage}
      onChange={(e) => void setLanguage(e.target.value)}
    >
      {languages.map((l) => (
        <option key={l} value={l}>
          {i18n.getFixedT(l)("language.name")}
        </option>
      ))}
    </select>
  );
}
