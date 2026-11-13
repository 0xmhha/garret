import { useMemo } from "react";
import { renderMarkdown } from "../markdown/markdown";

/**
 * Markdown shows member text. Its HTML comes only from renderMarkdown
 * (sanitized, and TrustedHTML under the CSP), the one sanctioned source.
 */
export function Markdown({ source, className = "md" }: { source: string; className?: string }) {
  const html = useMemo(() => renderMarkdown(source), [source]);
  return <div className={className} dangerouslySetInnerHTML={{ __html: html as string }} />;
}
