import { useState } from "react";
import { Bug, Copy, ExternalLink } from "lucide-react";
import { errorText } from "@/api/client";
import { useIssueDraft } from "@/api/hooks";
import { Field, inputCls, textareaCls } from "@/components/form";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { t } from "@/lib/copy";
import { issueBody, newIssueURL } from "@/lib/issue";

/**
 * ReportProblem is the Report a problem form of the help menu. It adds the redacted
 * diagnostics of the server and opens a filled new-issue page on GitHub. Nothing goes to
 * GitHub before the person submits it there.
 */
export function ReportProblem({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const draft = useIssueDraft(open);
  const [title, setTitle] = useState("");
  const [what, setWhat] = useState("");
  const [expected, setExpected] = useState("");
  const [steps, setSteps] = useState("");
  const [copied, setCopied] = useState(false);
  const d = draft.data;
  const body = d ? issueBody({ what, expected, steps }, d.markdown) : "";
  const link = d ? newIssueURL(d.repo, title.trim(), body, d.max_url) : null;
  const ok = !!link && title.trim().length >= 5 && what.trim() !== "";
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        onOpenChange(o);
        if (!o) setCopied(false);
      }}
    >
      <DialogContent className="max-h-[90vh] overflow-y-auto border-rule-strong bg-surface-raised text-ink">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-base">
            <Bug aria-hidden className="size-4 text-glaze" />
            {t("help.report.title")}
          </DialogTitle>
          <DialogDescription>{t("help.report.intro", { repo: d?.repo ?? "GitHub" })}</DialogDescription>
        </DialogHeader>
        {draft.isError && <p className="text-sm text-danger">{errorText(draft.error)}</p>}
        <form className="space-y-3" onSubmit={(e) => e.preventDefault()}>
          <Field id="issue-title" label={t("help.report.titleLabel")}>
            <input
              id="issue-title"
              value={title}
              maxLength={200}
              onChange={(e) => setTitle(e.target.value)}
              className={inputCls}
            />
          </Field>
          <Field id="issue-what" label={t("help.report.what")}>
            <textarea
              id="issue-what"
              rows={3}
              value={what}
              onChange={(e) => setWhat(e.target.value)}
              className={textareaCls}
            />
          </Field>
          <Field id="issue-expected" label={t("help.report.expected")}>
            <textarea
              id="issue-expected"
              rows={2}
              value={expected}
              onChange={(e) => setExpected(e.target.value)}
              className={textareaCls}
            />
          </Field>
          <Field id="issue-steps" label={t("help.report.steps")}>
            <textarea
              id="issue-steps"
              rows={3}
              value={steps}
              onChange={(e) => setSteps(e.target.value)}
              className={textareaCls}
            />
          </Field>
          <div>
            <p className="text-sm font-[var(--weight-medium)] text-ink">{t("help.report.diagnostics")}</p>
            <p className="text-xs text-ink-faint">{t("help.report.diagnosticsHint")}</p>
            <pre className="mt-1 max-h-40 overflow-auto rounded-sm border border-rule bg-surface-sunk p-2 font-mono text-xs whitespace-pre-wrap">
              {d ? d.markdown : t("help.report.loading")}
            </pre>
          </div>
          {link?.cut && <p className="text-xs text-ink-faint">{t("card.issue.cut")}</p>}
          <div className="flex flex-wrap items-center gap-2">
            <a
              href={ok ? link.url : undefined}
              target="_blank"
              rel="noreferrer"
              aria-disabled={!ok}
              onClick={(e) => {
                if (!ok) e.preventDefault();
              }}
              className={
                "inline-flex h-8 items-center gap-1.5 rounded-sm bg-glaze px-3 text-sm font-[var(--weight-medium)] text-glaze-ink " +
                (ok ? "hover:brightness-105" : "pointer-events-none opacity-50")
              }
            >
              <ExternalLink aria-hidden className="size-3.5" />
              {t("card.issue.open")}
            </a>
            <button
              type="button"
              disabled={!d}
              onClick={() => void navigator.clipboard?.writeText(body).then(() => setCopied(true))}
              className="inline-flex h-8 items-center gap-1.5 rounded-sm px-2 text-sm text-ink-soft hover:bg-surface-sunk hover:text-ink"
            >
              <Copy aria-hidden className="size-3.5" />
              {copied ? t("card.issue.copied") : t("card.issue.copy")}
            </button>
            {!ok && <span className="text-xs text-ink-faint">{t("help.report.need")}</span>}
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
