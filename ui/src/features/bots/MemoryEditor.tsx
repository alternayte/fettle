import { useState } from "react";
import { CircleAlert } from "lucide-react";
import { toast } from "sonner";
import { errorText } from "@/api/client";
import type { SchemaMemoryView } from "@/api/schema";
import { Notice } from "@/app/Page";
import { Btn, Bytes, textareaCls } from "@/components/form";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

const encoder = new TextEncoder();

function Editor({
  id,
  memory,
  label,
  onSave,
  saving,
}: {
  id: string;
  memory: SchemaMemoryView;
  label: string;
  onSave: (content: string) => Promise<unknown>;
  saving: boolean;
}) {
  const [text, setText] = useState(memory.content);
  const used = encoder.encode(text).length;
  const over = used > memory.limit;
  const dirty = text !== memory.content;
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (over || !dirty) return;
        onSave(text).then(
          () => toast.success(t("memory.saved")),
          (err) => toast.error(t("memory.saveFailed"), { description: errorText(err) }),
        );
      }}
      className="space-y-2"
    >
      <label htmlFor={id} className="sr-only">
        {label}
      </label>
      <textarea
        id={id}
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={10}
        spellCheck={false}
        aria-invalid={over || undefined}
        aria-describedby={`${id}-bytes`}
        placeholder={t("memory.placeholder")}
        className={cn(textareaCls, "min-h-48 resize-y font-mono text-sm leading-6")}
      />
      <div className="flex flex-wrap items-center gap-2">
        <span id={`${id}-bytes`}>
          <Bytes used={used} limit={memory.limit} />
        </span>
        <span className="figures text-ink-faint">· {t("memory.rev", { rev: memory.rev })}</span>
        <div className="ml-auto flex items-center gap-1.5">
          {dirty && (
            <Btn kind="quiet" size="sm" onClick={() => setText(memory.content)}>
              {t("common.reset")}
            </Btn>
          )}
          <Btn type="submit" kind="primary" size="sm" disabled={!dirty || over} busy={saving}>
            {t("common.save")}
          </Btn>
        </div>
      </div>
    </form>
  );
}

/** MemoryEditor edits one MEMORY.md with a byte counter against the server limit. */
export function MemoryEditor({
  id,
  label,
  query,
  onSave,
  saving,
}: {
  id: string;
  label: string;
  query: { data?: SchemaMemoryView; isPending: boolean; isError: boolean; error: unknown };
  onSave: (content: string) => Promise<unknown>;
  saving: boolean;
}) {
  if (query.isPending)
    return (
      <p className="text-sm text-ink-faint" role="status">
        {t("common.loading")}
      </p>
    );
  if (query.isError || !query.data)
    return (
      <Notice icon={CircleAlert} tone="danger" title={t("memory.loadFailed")}>
        {errorText(query.error)}
      </Notice>
    );
  return <Editor key={query.data.rev} id={id} label={label} memory={query.data} onSave={onSave} saving={saving} />;
}
