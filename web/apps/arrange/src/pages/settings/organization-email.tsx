import { useEffect, useRef, useState } from "react";
import { Check } from "lucide-react";
import { emails, siteName } from "@zonventure/api";
import { useT } from "../../i18n.tsx";
import { Card, EmailPreview, ErrorBox, Field, Loading, errorText, useLoad } from "../../ui/index.ts";
import "./organization-email.css";

/** Languages by their own names, as a picker shows them. */
const LANGUAGE_NAMES: Record<string, string> = { en: "English", sv: "Svenska", et: "Eesti" };

/**
 * The look of the organisation's emails: one of the platform's themes,
 * in the logo and colours from Branding. Hovering a theme previews it
 * without saving; choosing one saves at once, for admins. `brand`
 * changes whenever the logo or colours do, so the preview follows.
 */
export function EmailLookCard({ orgName, canEdit, brand }: { orgName: string; canEdit: boolean; brand: string }) {
  const t = useT();
  const catalog = useLoad(() => emails.themes(), []);
  const current = useLoad(() => emails.orgTheme(), []);
  const [hover, setHover] = useState<emails.Theme | null>(null);
  // The language the preview shows, the organisation's to begin with.
  const [lang, setLang] = useState<string | null>(null);
  const [kind, setKind] = useState("");
  const [err, setErr] = useState("");
  const [chip, setChip] = useState(false);
  const chipTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(chipTimer.current), []);

  const kinds = catalog.data?.kinds ?? [];
  useEffect(() => {
    if (!kind && kinds[0]) setKind(kinds[0].id);
  }, [kind, kinds]);

  const theme = current.data?.theme;
  const viewed = hover ?? theme;
  const shownLang = lang ?? current.data?.language ?? "en";
  const preview = useLoad(
    (signal) => (kind && viewed ? emails.preview(kind, { theme: viewed, lang: shownLang, signal }) : Promise.resolve(null)),
    [kind, viewed, shownLang, brand],
  );

  const saveLanguage = async (language: string) => {
    if (!canEdit || !current.data || language === current.data.language) return;
    const before = current.data;
    current.set({ ...before, language });
    setLang(null);
    setErr("");
    try {
      await emails.setOrgLanguage(language);
      setChip(true);
      clearTimeout(chipTimer.current);
      chipTimer.current = setTimeout(() => setChip(false), 1500);
    } catch (e) {
      current.set(before);
      setErr(errorText(e));
    }
  };

  const choose = async (id: emails.Theme) => {
    if (!canEdit || id === theme || !current.data) return;
    const before = current.data;
    current.set({ ...before, theme: id });
    setErr("");
    try {
      await emails.setOrgTheme(id);
      setChip(true);
      clearTimeout(chipTimer.current);
      chipTimer.current = setTimeout(() => setChip(false), 1500);
    } catch (e) {
      current.set(before);
      setErr(errorText(e));
    }
  };

  const failed = catalog.error || current.error;
  const body = failed ? (
    <ErrorBox message={failed} retry={() => { catalog.reload(); current.reload(); }} retryLabel={t.t("Retry")} />
  ) : !catalog.data || !current.data ? (
    <Loading label={t.t("Loading…")} />
  ) : (
    <div className="og-mail">
      <div className="og-controls">
        <section className="og-section">
          <h3>
            {t.t("Theme")}{" "}
            <span className={"og-chip" + (chip ? " is-on" : "")} aria-hidden={!chip} role="status">{chip ? t.t("Saved") : ""}</span>
          </h3>
          <div className="og-themes" role="radiogroup" aria-label={t.t("Email theme")} onMouseLeave={() => setHover(null)}>
            {catalog.data.themes.map((th) => (
              <button
                key={th.id}
                type="button"
                role="radio"
                aria-checked={th.id === theme}
                aria-disabled={!canEdit || undefined}
                className={"og-theme" + (th.id === theme ? " is-current" : "") + (th.id === viewed ? " is-viewed" : "")}
                onMouseEnter={() => setHover(th.id)}
                onFocus={() => setHover(th.id)}
                onBlur={() => setHover(null)}
                onClick={() => void choose(th.id)}
              >
                <span className="og-theme__name">
                  {t.t(th.name)}
                  {th.id === theme && <Check size={14} aria-hidden />}
                </span>
                <span className="og-theme__desc">{t.t(th.description)}</span>
              </button>
            ))}
          </div>
          {!canEdit && <p className="cs-note">{t.t("Only organization admins can change the email theme.")}</p>}
          <p className="cs-note">{t.t("Emails use the logo and colors from Branding above. Each event can choose its own theme.")}</p>
          {err && <p className="cs-err" role="alert">{err}</p>}
        </section>
        <section className="og-section">
          <h3>{t.t("Language")}</h3>
          <Field label={t.t("Emails outside events")} hint={t.t("Such as box redemptions. An event's emails are in its adventure's language; designers get theirs in the language they use {{name}} in.", { name: siteName() })}>
            <select value={current.data.language} disabled={!canEdit} onChange={(e) => void saveLanguage(e.target.value)}>
              {current.data.languages.map((l) => (
                <option key={l} value={l}>{LANGUAGE_NAMES[l] ?? l}</option>
              ))}
            </select>
          </Field>
        </section>
      </div>

      <div className="og-preview">
        <div className="og-preview__head">
        <Field label={t.t("Email to preview")}>
          <select value={kind} onChange={(e) => setKind(e.target.value)}>
            {(["players", "designers"] as const).map((a) => {
              const group = kinds.filter((k) => k.audience === a);
              if (!group.length) return null;
              return (
                <optgroup key={a} label={a === "players" ? t.t("To players") : t.t("To designers")}>
                  {group.map((k) => <option key={k.id} value={k.id}>{t.t(k.name)}</option>)}
                </optgroup>
              );
            })}
          </select>
        </Field>
        <Field label={t.t("Preview in")}>
          <select value={shownLang} onChange={(e) => setLang(e.target.value)}>
            {current.data.languages.map((l) => (
              <option key={l} value={l}>{LANGUAGE_NAMES[l] ?? l}</option>
            ))}
          </select>
        </Field>
        </div>
        {preview.error ? <p className="cs-err" role="alert">{preview.error}</p> : <EmailPreview preview={preview.data} loading={preview.loading} from={orgName} onSendTest={() => emails.sendMeTest({ sample: kind, lang: shownLang, ...(viewed ? { theme: viewed } : {}) })} />}
      </div>
    </div>
  );

  return (
    <Card title={t.t("Email theme")} subtitle={t.t("How the emails your organization sends to players and designers look.")}>
      {body}
    </Card>
  );
}
