import { useEffect, useRef, useState, type FormEvent } from "react";
import { ApiError, play } from "@zonventure/api";
import { RoleCards, type Translate } from "@zonventure/play-ui";
import type { I18n } from "@zonventure/i18n";
import { Files } from "./Files.tsx";

/** Each language in its own words, so a player finds their own. */
const LANG_NAMES: Record<string, string> = { sv: "Svenska", en: "English", et: "Eesti", de: "Deutsch", fi: "Suomi", no: "Norsk", da: "Dansk", fr: "Français", es: "Español" };

/**
 * The player's menu: who they are on the team, the team's name, their
 * roles, the organisers' files, the app's language, location sharing, and their data — sign out on a
 * shared phone, or erase it all. Beside the game; nothing here moves
 * the team on.
 *
 * A drawer that folds in from the left edge. The adventure's picture at
 * the bar's left end opens it (a ☰ when there is no picture) and turns
 * into the ✕ that closes it, staying above the drawer, so open and close
 * are the same place under the thumb.
 */
export function Menu(props: {
  state: play.PlayState;
  t: Translate;
  i18n: I18n;
  usesLocation: boolean;
  shareLocation: boolean;
  onShareLocation: (on: boolean) => void;
  onRenamed: () => void;
  onLanguage: (lang: string) => void;
  leave: (erase: boolean) => Promise<void>;
  /** The adventure's own picture, else the organisation's logo. */
  icon?: string | undefined;
}) {
  const { state, t, i18n } = props;
  const [open, setOpen] = useState(false);
  // Each opening starts from the current names, not an old draft.
  const [opened, setOpened] = useState(0);
  // Which account action is waiting for a yes: both end the game on
  // this phone, so neither happens on a single tap.
  const [confirm, setConfirm] = useState<"signout" | "erase" | null>(null);
  const button = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string | null>(null);
  const [leaving, setLeaving] = useState(false);
  // A translated adventure: this player reads it in one of its languages,
  // and the app's words follow where the app has them. Otherwise only the
  // app's words change.
  const advLangs = state.view?.languages ?? [];
  const translated = advLangs.length > 1;
  const langs = translated ? advLangs : i18n.available();
  const lang = translated ? (state.view?.lang ?? advLangs[0]!) : i18n.current();

  const leave = async (erase: boolean) => {
    setLeaving(true);
    setError(null);
    try {
      await props.leave(erase);
    } catch (err) {
      setError(err instanceof ApiError ? t("Something went wrong. Try again.") : t("Couldn't reach the server. Check your connection and try again."));
      setLeaving(false);
    }
  };

  const close = () => {
    setOpen(false);
    setConfirm(null);
    button.current?.focus();
  };
  const toggle = () => {
    if (open) {
      close();
      return;
    }
    setOpened((n) => n + 1);
    setOpen(true);
  };

  // Escape closes; focus goes into the drawer, and Tab stays within the
  // drawer and its ✕ while it is open.
  useEffect(() => {
    if (!open) return;
    // The drawer itself, not its first field: focusing an input would
    // pop the phone's keyboard over the menu the player just opened.
    panel.current?.focus({ preventScroll: true });
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        close();
        return;
      }
      if (e.key !== "Tab" || !panel.current || !button.current) return;
      const items = [button.current, ...panel.current.querySelectorAll<HTMLElement>("input, select, button:not(:disabled), a[href]")];
      const i = items.indexOf(document.activeElement as HTMLElement);
      const next = e.shiftKey ? (i <= 0 ? items.length - 1 : i - 1) : (i === items.length - 1 ? 0 : i + 1);
      items[next]?.focus();
      e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  return (
    <>
      <button
        ref={button}
        type="button"
        className={"app-burger" + (props.icon ? " app-burger--icon" : "") + (open ? " is-open" : "")}
        onClick={toggle}
        aria-label={open ? t("Close menu") : t("Menu")}
        aria-expanded={open}
        aria-controls="app-menu"
      >
        {props.icon && <img className="app-burger__icon" src={props.icon} alt="" />}
        <span className="app-burger__lines" aria-hidden="true">
          <span />
          <span />
          <span />
        </span>
      </button>
      <div className={"app-drawer-back" + (open ? " is-open" : "")} onClick={close} aria-hidden="true" />
      <div
        id="app-menu"
        ref={panel}
        className={"app-drawer" + (open ? " is-open" : "")}
        role="dialog"
        aria-modal="true"
        aria-label={t("Menu")}
        tabIndex={-1}
        inert={!open}
      >
            <h2 className="app-drawer__title">{t("Menu")}</h2>
            <div className="app-menu" key={opened}>
              {state.team.locked && <p className="app-menu__locked">{lockedNote(t)}</p>}
              <NameField
                label={t("Your name")}
                value={state.me?.name ?? ""}
                t={t}
                save={play.renameMe}
                onSaved={props.onRenamed}
                locked={!!state.team.locked}
                hint={t("Shown to your team and the organisers.")}
              />
              <NameField
                label={t("Team name")}
                value={state.team.name}
                t={t}
                save={play.renameTeam}
                onSaved={props.onRenamed}
                locked={!!state.team.locked}
                hint={t("Everyone on the team sees the change.")}
              />
              <TeamPicture team={state.team} t={t} onSaved={props.onRenamed} />
              {(state.view?.roles?.length ?? 0) > 0 && <Roles roles={state.view?.roles ?? []} choosing={(state.view?.blocks ?? []).some((b) => b.kind === "roles" && b.choose && !b.locked)} t={t} teamName={state.team.name} onChanged={props.onRenamed} />}

              <Files files={state.event.files ?? []} t={t} locale={i18n.current()} />

              {langs.length > 1 && (
                <label className="app-menu__field">
                  <span className="app-menu__label">{t("Language")}</span>
                  <select className="pv-input" value={lang} onChange={(e) => (translated ? props.onLanguage(e.target.value) : i18n.setLocale(e.target.value))}>
                    {langs.map((l) => (
                      <option key={l} value={l}>{LANG_NAMES[l] ?? l.toUpperCase()}</option>
                    ))}
                  </select>
                </label>
              )}

              {props.usesLocation && (
                <label className="app-menu__toggle">
                  <input type="checkbox" checked={props.shareLocation} onChange={(e) => props.onShareLocation(e.target.checked)} />
                  <span>
                    <span className="app-menu__label">{t("Share location")}</span>
                    <span className="app-menu__hint">
                      {t("Off pauses your GPS pings. Gated puzzles need this on to detect arrival.")}{" "}
                      {t("It's deleted automatically after the event (within 30 days).")}
                    </span>
                  </span>
                </label>
              )}

              <div className="app-menu__account">
                <span className="app-menu__label">{t("Account")}</span>
                {confirm === "signout" ? (
                  <div className="app-menu__confirm app-menu__confirm--calm" role="alert">
                    <p>{t("Sign out on this phone? You'll need your link to get back into the game.")}</p>
                    <div className="app-menu__row">
                      <button type="button" className="pv-btn" onClick={() => setConfirm(null)}>{t("Cancel")}</button>
                      <button type="button" className="pv-btn pv-btn--primary" disabled={leaving} onClick={() => void leave(false)}>{t("Sign out")}</button>
                    </div>
                  </div>
                ) : (
                  <>
                    <button type="button" className="pv-btn pv-btn--wide" onClick={() => setConfirm("signout")}>
                      {t("Sign out on this phone")}
                    </button>
                    <span className="app-menu__hint">{t("Your link still works, so you can sign in again with it.")}</span>
                  </>
                )}
                {confirm === "erase" ? (
                  <div className="app-menu__confirm" role="alert">
                    <p>{t("This wipes your location history and your answers from this event. Other team members keep playing. You'll be signed out and the link won't work again. Continue?")}</p>
                    <div className="app-menu__row">
                      <button type="button" className="pv-btn" onClick={() => setConfirm(null)}>{t("Cancel")}</button>
                      <button type="button" className="pv-btn app-menu__danger" disabled={leaving} onClick={() => void leave(true)}>{t("Erase")}</button>
                    </div>
                  </div>
                ) : (
                  <>
                    <button type="button" className="pv-btn pv-btn--wide app-menu__danger" onClick={() => setConfirm("erase")}>
                      {t("Erase my play data")}
                    </button>
                    <span className="app-menu__hint">
                      {t("Permanently deletes your location pings and answer attempts from this event and revokes your link. Use at end of event or for privacy reasons.")}
                    </span>
                  </>
                )}
                {error && <p className="app-menu__error" role="alert">{error}</p>}
              </div>

              <p className="app-menu__foot">
                <a href="/privacy" target="_blank" rel="noopener">{t("Privacy")}</a>
                {" · "}
                <a href="/terms" target="_blank" rel="noopener">{t("Terms")}</a>
              </p>
            </div>
      </div>
    </>
  );
}

/**
 * The adventure's roles as portraits: who holds each, what the player's
 * own are for; tapping one takes it or gives it back. A role nobody took
 * is dealt, so taking one only says "this one is mine"; a teammate's
 * chosen role is theirs until they give it back, which the team sorts
 * out aloud.
 */
function Roles(props: { roles: play.RoleView[]; choosing: boolean; t: Translate; teamName: string; onChanged: () => void }) {
  const { roles, t } = props;
  const final = roles.some((r) => r.final);
  return (
    <div className="app-menu__field">
      <span className="app-menu__label">{t("Roles")}</span>
      <span className="app-menu__hint">{final ? t("Your team's roles are final.") : t("Each role sees something the others don't. Roles nobody chooses are shared out.")}</span>
      <RoleCards
        roles={roles}
        t={t}
        teamName={props.teamName}
        // On a step where the players choose, a role nobody chose is Free, as the step shows it.
        dealt={!props.choosing}
        onChoose={async (role, claim) => {
          try {
            await play.claimRole(role, claim);
          } finally {
            props.onChanged();
          }
        }}
      />
    </div>
  );
}

/** Why the names and the picture can no longer be changed here. */
const lockedNote = (t: Translate) => t("Your name, the team name and the team picture are fixed now that your team has begun. Ask the organisers if one needs changing.");

/** A refusal because the team has begun: the state is behind, so refresh it. */
const isLocked = (err: unknown) => err instanceof ApiError && err.code === "team_started";

export function NameField(props: {
  label: string;
  value: string;
  hint: string;
  t: Translate;
  save: (name: string) => Promise<unknown>;
  onSaved: () => void;
  /** The team has begun: shown, not editable. */
  locked: boolean;
}) {
  const { t } = props;
  const [draft, setDraft] = useState(props.value);
  const [state, setState] = useState<"idle" | "saving" | "saved">("idle");
  const [error, setError] = useState<string | null>(null);
  const name = draft.trim();
  const changed = name !== "" && name !== props.value;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!changed) return;
    setState("saving");
    setError(null);
    try {
      await props.save(name);
      setState("saved");
      props.onSaved();
    } catch (err) {
      setState("idle");
      if (isLocked(err)) {
        setDraft(props.value);
        props.onSaved();
        return;
      }
      setError(
        err instanceof ApiError && err.status === 409
          ? t("Another team already has that name.")
          : err instanceof ApiError && err.status === 400
            ? t("Use 1 to 64 characters.")
            : t("Couldn't reach the server. Check your connection and try again."),
      );
    }
  };

  return (
    <form className="app-menu__field" onSubmit={(e) => void submit(e)}>
      <label className="app-menu__label" htmlFor={props.label}>{props.label}</label>
      <div className="app-menu__row">
        <input
          id={props.label}
          className="pv-input"
          value={props.locked ? props.value : draft}
          maxLength={64}
          autoComplete="off"
          enterKeyHint="done"
          readOnly={props.locked}
          onChange={(e) => { setDraft(e.target.value); setState("idle"); }}
        />
        {!props.locked && (
          <button type="submit" className="pv-btn pv-btn--primary" disabled={!changed || state === "saving"}>
            {state === "saved" && !changed ? t("Saved") : t("Save")}
          </button>
        )}
      </div>
      {!props.locked && <span className="app-menu__hint">{error ? <span className="app-menu__error">{error}</span> : props.hint}</span>}
    </form>
  );
}

/**
 * The team's picture: on the leaderboard and the organisers' map. Any
 * member may set or remove it until the team begins.
 */
function TeamPicture({ team, t, onSaved }: { team: play.PlayState["team"]; t: Translate; onSaved: () => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The picture keeps its address when it changes; a version defeats the cache.
  const [version, setVersion] = useState(0);
  const src = team.avatar_url ? `${team.avatar_url}${version ? `?v=${version}` : ""}` : null;

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      setVersion(Date.now());
      onSaved();
    } catch (err) {
      if (isLocked(err)) onSaved();
      else setError(err instanceof ApiError && err.status === 413 ? t("That picture is too large (5 MB at most).") : err instanceof ApiError && err.status === 400 ? t("That file isn't a picture we can use.") : t("Couldn't reach the server. Check your connection and try again."));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="app-menu__field">
      <span className="app-menu__label">{t("Team picture")}</span>
      <div className="app-menu__row app-menu__picture">
        {team.locked ? (
          <span className="app-team-pic" aria-hidden={!src}>
            {src ? <img src={src} alt={t("Your team's picture")} /> : <span>{(team.name.trim()[0] ?? "?").toUpperCase()}</span>}
          </span>
        ) : (
          <button
            type="button"
            className={"app-pic-drop" + (src ? " has-image" : "")}
            disabled={busy}
            aria-label={src ? t("Change") : t("Choose a picture")}
            onClick={() => input.current?.click()}
          >
            {src ? <img src={src} alt="" /> : <span>{t("Tap to choose a picture")}</span>}
          </button>
        )}
        {!team.locked && (
          <>
            {src && (
              <button type="button" className="pv-btn" disabled={busy} onClick={() => void run(play.clearTeamAvatar)}>
                {t("Remove")}
              </button>
            )}
            <input
              ref={input}
              type="file"
              accept="image/*"
              hidden
              onChange={(e) => {
                const file = e.target.files?.[0];
                e.target.value = "";
                if (file) void run(() => play.setTeamAvatar(file));
              }}
            />
          </>
        )}
      </div>
      {!team.locked && (
        <span className="app-menu__hint">{error ? <span className="app-menu__error">{error}</span> : t("Shown on the leaderboard and to the organisers.")}</span>
      )}
    </div>
  );
}
