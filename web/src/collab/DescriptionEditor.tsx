import { defaultKeymap, indentWithTab } from "@codemirror/commands";
import { EditorView, keymap, placeholder } from "@codemirror/view";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { yCollab, yUndoManagerKeymap } from "y-codemirror.next";
import { Awareness } from "y-protocols/awareness";
import * as Y from "yjs";
import { useApi, useT } from "../app/context";
import type { MessageKey } from "../i18n/i18n";
import { Markdown } from "../ui/Markdown";
import { connectDoc } from "./provider";
import "./collab.css";

const rejoinDelay = 2000;

// The editor lives in a shadow root: there CodeMirror's style module uses
// constructed style sheets instead of a <style> element, which the CSP
// (style-src 'self') would block. Its look comes from this theme, built on
// the page's custom properties, which do cross the shadow boundary.
const theme = EditorView.theme({
  "&": { minHeight: "120px", color: "var(--fg)", backgroundColor: "var(--bg)" },
  ".cm-scroller": { fontFamily: "var(--font-ui)", fontSize: "var(--text-md)", lineHeight: "1.6" },
  "&.cm-focused": { outline: "2px solid var(--focus)", outlineOffset: "-2px" },
  ".cm-content": { padding: "12px 16px", caretColor: "var(--accent)" },
  ".cm-placeholder": { color: "var(--fg-muted)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection": { backgroundColor: "var(--selection)" },
  ".cm-cursor": { borderLeftColor: "var(--accent)" },
});

/**
 * DescriptionEditor edits a collaborative document (a review description or
 * a discussion body) with everyone who has it open. It shows how many others
 * are editing but not who, and draws no remote cursors: awareness data is
 * relayed unchecked, so it never stands for identity, and the cursor
 * colours would need inline style attributes the CSP refuses.
 */
export function DescriptionEditor({ project, doc, label }: { project: string; doc: string; label: string }) {
  const api = useApi();
  const t = useT();
  const [session, setSession] = useState(0);
  const [mode, setMode] = useState<"edit" | "preview">("edit");
  const [text, setText] = useState("");
  const [status, setStatus] = useState<"connecting" | "live" | "offline">("connecting");
  const [others, setOthers] = useState(0);
  const host = useRef<HTMLDivElement>(null);
  const info = useQuery({
    queryKey: ["collab", project, doc, session],
    queryFn: ({ signal }) =>
      api.request<{ generation: number; text_name: string }>("GET", `/api/v1/projects/${encodeURIComponent(project)}/collab/${encodeURIComponent(doc)}/info`, undefined, { signal }),
    staleTime: 0,
  });

  useEffect(() => {
    if (!info.data || !host.current) return;
    const ydoc = new Y.Doc();
    const ytext = ydoc.getText(info.data.text_name);
    const awareness = new Awareness(ydoc);
    awareness.setLocalState({ present: true });
    const countOthers = () => setOthers(Math.max(0, awareness.getStates().size - 1));
    awareness.on("change", countOthers);
    const undo = new Y.UndoManager(ytext);
    const shadow = host.current.shadowRoot ?? host.current.attachShadow({ mode: "open" });
    const mount = document.createElement("div");
    shadow.replaceChildren(mount);
    const view = new EditorView({
      root: shadow,
      parent: mount,
      doc: ytext.toString(),
      extensions: [
        theme,
        keymap.of([...yUndoManagerKeymap, ...defaultKeymap, indentWithTab]),
        EditorView.lineWrapping,
        EditorView.contentAttributes.of({ "aria-label": label }),
        placeholder(t("collab.placeholder")),
        // No awareness: remote selections would need inline style attributes.
        yCollab(ytext, null, { undoManager: undo }),
      ],
    });
    const sync = () => setText(ytext.toString());
    ytext.observe(sync);
    let timer: ReturnType<typeof setTimeout> | undefined;
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${proto}//${location.host}/api/v1/projects/${encodeURIComponent(project)}/collab/${encodeURIComponent(doc)}?generation=${info.data.generation}`;
    const leave = connectDoc({
      url,
      doc: ydoc,
      awareness,
      WebSocket: window.WebSocket as never,
      onReload: () => setSession((s) => s + 1),
      onOpen: () => setStatus("live"),
      onClose: (code) => {
        setStatus("offline");
        if (code !== 1000) timer = setTimeout(() => setSession((s) => s + 1), rejoinDelay);
      },
    });
    setStatus("connecting");
    return () => {
      clearTimeout(timer);
      awareness.off("change", countOthers);
      leave();
      ytext.unobserve(sync);
      view.destroy();
      undo.destroy();
      ydoc.destroy();
    };
  }, [info.data, project, doc, label, t]);

  return (
    <div className="collab">
      <div className="collab__bar">
        <div className="collab__modes" role="group" aria-label={label}>
          <button type="button" aria-pressed={mode === "edit"} onClick={() => setMode("edit")}>
            {t("collab.edit")}
          </button>
          <button type="button" aria-pressed={mode === "preview"} onClick={() => setMode("preview")}>
            {t("collab.preview")}
          </button>
        </div>
        {status === "live" && others > 0 ? <span className="collab__others">{t("collab.others", { count: others })}</span> : null}
        <span className={`collab__status collab__status--${status}`}>{t(`collab.status.${status}` as MessageKey)}</span>
      </div>
      <div ref={host} className="collab__editor" hidden={mode !== "edit"} />
      {mode === "preview" ? <div className="collab__preview">{text.trim() ? <Markdown source={text} /> : <p className="muted">{t("collab.empty")}</p>}</div> : null}
      {info.isError ? <p className="form-error" role="alert">{t("collab.failed")}</p> : null}
    </div>
  );
}
