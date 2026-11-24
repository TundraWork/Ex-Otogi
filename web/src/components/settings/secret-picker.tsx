import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { useRef, useState } from "react";
import { Ago } from "@/components/ago";
import { LoadError } from "@/components/load-error";
import { SecretDialog } from "@/components/secret-dialog";
import { StatusDot } from "@/components/status-dot";
import { Select, SelectItem, SelectPopup, SelectSeparator, SelectTrigger, SelectValue } from "@/components/ui/select";
import { choices, reference, referenceName, type Secret, sources } from "@/lib/secrets";
import { secretsQuery } from "@/queries/secrets";
import type { Labelling } from "./schema-form";

// No secret has this name: choosing it opens the dialog that adds one.
const adding = "+";

function Choice({ secret }: { secret: Secret }) {
  const source = sources[secret.source];
  return (
    <span className="flex min-w-0 items-center gap-3">
      <span className="min-w-0 flex-1 truncate font-mono text-[13px]">{secret.name}</span>
      <span className="flex flex-none items-center gap-2 text-muted-foreground text-xs">
        <StatusDot tone={source.tone} />
        {source.label}
      </span>
    </span>
  );
}

/** SecretPicker chooses the secret a field refers to. The field holds the
 * reference, ${NAME}; the value is on the Secrets page's side. */
export function SecretPicker({
  value,
  onChange,
  onBlur,
  labelling: a,
}: {
  value: string;
  onChange: (value: string) => void;
  onBlur?: () => void;
  labelling: Labelling;
}) {
  const { data: secrets = [], error, refetch } = useQuery(secretsQuery);
  const [add, setAdd] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const name = referenceName(value);
  const known = choices(secrets, name);
  return (
    <>
      <Select
        value={name}
        onValueChange={(next) => {
          // Not a choice: when the list changes, the select gives up a value
          // it had no item for, which is how a secret just added arrives.
          if (next === null) return;
          if (next === adding) setAdd(true);
          else onChange(next ? reference(next) : "");
        }}
      >
        <SelectTrigger {...a} ref={trigger} onBlur={onBlur}>
          <SelectValue>
            {(chosen: string) => {
              const secret = known.find((s) => s.name === chosen);
              return secret ? (
                <Choice secret={secret} />
              ) : (
                <span className="text-muted-foreground">Choose a secret</span>
              );
            }}
          </SelectValue>
        </SelectTrigger>
        <SelectPopup aria-labelledby={a["aria-labelledby"]}>
          {!a["aria-required"] && <SelectItem value="">None</SelectItem>}
          {known.map((s) => (
            <SelectItem key={s.name} value={s.name}>
              <Choice secret={s} />
            </SelectItem>
          ))}
          <SelectSeparator />
          <SelectItem value={adding}>
            <span className="flex items-center gap-2">
              <Plus className="size-4" /> Add secret…
            </span>
          </SelectItem>
        </SelectPopup>
      </Select>
      {error && <LoadError what="the secrets" error={error} retry={() => void refetch()} />}
      <SecretDialog
        open={add}
        onOpenChange={setAdd}
        onSaved={(added) => onChange(reference(added))}
        finalFocus={trigger}
      />
    </>
  );
}

/** SecretNote says where the secret a field refers to has its value. */
export function SecretNote({ value }: { value: unknown }) {
  const { data: secrets } = useQuery(secretsQuery);
  const name = referenceName(value);
  if (!name || !secrets) return null;
  const secret = secrets.find((s) => s.name === name);
  if (secret?.source === "panel") {
    return (
      <>
        Set in the panel by {secret.by || "someone unknown"}
        {secret.at && (
          <>
            {" · "}
            <Ago at={secret.at} />
          </>
        )}
      </>
    );
  }
  if (secret?.source === "environment") return "Read from the environment variable of this name.";
  return (
    <span className="text-destructive-foreground">Set nowhere: what uses it cannot be built until it has a value.</span>
  );
}
