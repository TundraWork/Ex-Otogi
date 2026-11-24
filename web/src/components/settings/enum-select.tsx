import type { ReactNode } from "react";
import { Select, SelectItem, SelectPopup, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { Labelling } from "./schema-form";

export type EnumSelectProps = {
  labelling: Labelling;
  onBlur?: () => void;
  value: string;
  onChange: (value: string) => void;
  /** items are the choices, each shown as its label in the list and, once
   * chosen, as its chosen label when it has one, else its label. */
  items: { value: string; label: ReactNode; chosen?: ReactNode }[];
  /** none labels the choice of nothing, "", when there is one. */
  none?: ReactNode;
  /** placeholder stands for a value no item has. */
  placeholder?: string;
};

/** EnumSelect chooses one of a field's choices. */
export function EnumSelect({ labelling: a, onBlur, value, onChange, items, none, placeholder }: EnumSelectProps) {
  const all = none === undefined ? items : [{ value: "", label: none }, ...items];
  return (
    <Select
      value={value}
      onValueChange={(next) => {
        // Not a choice: when the items change, the select gives up a value
        // it had no item for.
        if (next !== null) onChange(next);
      }}
    >
      <SelectTrigger {...a} onBlur={onBlur}>
        <SelectValue>
          {(chosen: string) =>
            ((item) => item && (item.chosen ?? item.label))(all.find((i) => i.value === chosen)) ?? (
              <span className="text-muted-foreground">{placeholder}</span>
            )
          }
        </SelectValue>
      </SelectTrigger>
      <SelectPopup aria-labelledby={a["aria-labelledby"]}>
        {all.map((item) => (
          <SelectItem key={item.value} value={item.value}>
            {item.label}
          </SelectItem>
        ))}
      </SelectPopup>
    </Select>
  );
}
