import { useMemo } from "react";
import { inputCls } from "@/components/form";
import { cn } from "@/lib/utils";

/** TimeZoneInput is a text field with the IANA zone names of this browser as suggestions. */
export function TimeZoneInput({
  id,
  value,
  onChange,
  describedBy,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  describedBy?: string;
}) {
  const zones = useMemo(() => {
    const f = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf;
    try {
      return f ? f("timeZone") : [];
    } catch {
      return [];
    }
  }, []);
  return (
    <>
      <input
        id={id}
        value={value}
        list={`${id}-zones`}
        spellCheck={false}
        autoComplete="off"
        aria-describedby={describedBy}
        onChange={(e) => onChange(e.target.value)}
        className={cn(inputCls, "font-mono text-sm")}
      />
      <datalist id={`${id}-zones`}>
        {zones.map((z) => (
          <option key={z} value={z} />
        ))}
      </datalist>
    </>
  );
}
