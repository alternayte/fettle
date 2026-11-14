import { memo } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

// Bot output is untrusted. Raw HTML becomes plain text, and only http, https and
// mailto links are active (SI-15).
const SAFE_SCHEMES = /^(https?:|mailto:)/i;

type MdNode = { type: string; value?: string; children?: MdNode[] };

function htmlAsText() {
  return (tree: MdNode) => {
    const walk = (node: MdNode) => {
      if (!node.children) return;
      for (const child of node.children) {
        if (child.type === "html") child.type = "text";
        walk(child);
      }
    };
    walk(tree);
  };
}

export function isSafeHref(href: string | undefined): boolean {
  if (!href) return false;
  const trimmed = href.trim();
  if (trimmed.startsWith("#") || trimmed.startsWith("/")) return true;
  return SAFE_SCHEMES.test(trimmed);
}

const components: Components = {
  a({ href, children }) {
    if (!isSafeHref(href)) {
      return (
        <a data-inactive="" aria-disabled="true" title={href}>
          {children}
        </a>
      );
    }
    const external = /^https?:/i.test(href ?? "");
    return (
      <a href={href} {...(external ? { target: "_blank", rel: "noopener noreferrer nofollow" } : {})}>
        {children}
      </a>
    );
  },
  img({ alt }) {
    return <span className="text-ink-faint">[{alt || "image"}]</span>;
  },
};

// The original href is kept so the component above can decide; react-markdown's
// default transform would blank unsafe links before we see them.
const keepUrl = (url: string) => url;

export const Markdown = memo(function Markdown({ text }: { text: string }) {
  return (
    <div className="prose-log">
      <ReactMarkdown remarkPlugins={[remarkGfm, htmlAsText]} components={components} urlTransform={keepUrl}>
        {text}
      </ReactMarkdown>
    </div>
  );
});
