import { useState } from "react";
import { Check, ChevronsUpDown } from "lucide-react";
import type { SchemaModelInfo } from "@/api/schema";
import { inputCls } from "@/components/form";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { t } from "@/lib/copy";
import { cn } from "@/lib/utils";

function price(m: SchemaModelInfo): string | null {
  if (!m.price_in && !m.price_out) return null;
  const per = (v?: number) => `$${(v ?? 0).toFixed(2)}`;
  return t("models.pricePerM", { in: per(m.price_in), out: per(m.price_out) });
}

/** ModelPicker is a searchable list of the models of a provider. A typed name that is not in the list is allowed. */
export function ModelPicker({
  id,
  models,
  value,
  onChange,
  disabled,
  describedBy,
}: {
  id: string;
  models: SchemaModelInfo[];
  value: string;
  onChange: (id: string) => void;
  disabled?: boolean;
  describedBy?: string;
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const typed = search.trim();
  const exact = models.some((m) => m.id === typed);
  const pick = (v: string) => {
    onChange(v);
    setOpen(false);
    setSearch("");
  };
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          id={id}
          type="button"
          role="combobox"
          aria-expanded={open}
          aria-describedby={describedBy}
          disabled={disabled}
          className={cn(inputCls, "flex items-center gap-2 text-left font-mono text-sm")}
        >
          <span className={cn("min-w-0 flex-1 truncate", !value && "text-ink-faint")}>
            {value || t("models.pickModel")}
          </span>
          <ChevronsUpDown aria-hidden className="size-3.5 shrink-0 text-ink-faint" />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-[min(28rem,calc(100vw-2rem))] border-rule-strong bg-surface-raised p-0"
      >
        <Command>
          <CommandInput value={search} onValueChange={setSearch} placeholder={t("models.searchModel")} />
          <CommandList>
            <CommandEmpty>{t("models.noModels")}</CommandEmpty>
            {typed && !exact && (
              <CommandGroup>
                <CommandItem value={`custom:${typed}`} onSelect={() => pick(typed)}>
                  <span className="text-sm">{t("models.useTyped", { model: typed })}</span>
                </CommandItem>
              </CommandGroup>
            )}
            <CommandGroup>
              {models.map((m) => (
                <CommandItem key={m.id} value={m.id} onSelect={() => pick(m.id)}>
                  <Check aria-hidden className={cn("size-3.5", m.id === value ? "opacity-100" : "opacity-0")} />
                  <span className="min-w-0 flex-1 truncate font-mono text-xs">{m.id}</span>
                  {price(m) && <span className="figures shrink-0 text-ink-faint">{price(m)}</span>}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
