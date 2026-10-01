import { useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { PlusIcon } from "lucide-react";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { Flag } from "@/components/country";
import { FormError, FormField } from "@/components/form-field";
import { MultiSelect, type MultiSelectOption } from "@/components/multi-select";
import { useAddLanes } from "@/hooks/use-lanes";
import { useSurfshark, useSurfsharkLocations } from "@/hooks/use-providers";
import { errorMessage, fieldErrors } from "@/lib/api";
import type { Lane } from "@/lib/types";

/** Pick Surfshark locations (any country or city) and add them as lanes. */
export function AddLanesDialog({ lanes }: { lanes: Lane[] }) {
  const [open, setOpen] = useState(false);
  const [picked, setPicked] = useState<string[]>([]);
  const locations = useSurfsharkLocations(open);
  const provider = useSurfshark();
  const add = useAddLanes();
  const hasKeys = (provider.data?.keys ?? []).some((k) => k.enabled);

  const active = useMemo(() => new Set(lanes.map((l) => l.id)), [lanes]);
  const options = useMemo<MultiSelectOption[]>(
    () =>
      (locations.data ?? [])
        .filter((l) => !active.has(`surfshark:${l.id}`))
        .map((l) => ({
          value: l.id,
          label: `${l.city}, ${l.country}${l.virtual ? " (virtual)" : ""}`,
          icon: <Flag code={l.countryCode} />,
          hint: `${Math.round(l.load)}% load`,
          keywords: [l.id, l.countryCode, l.city, l.country],
        }))
        .sort((a, b) => a.label.localeCompare(b.label)),
    [locations.data, active],
  );

  const server = fieldErrors(add.error);
  const general =
    add.error && Object.keys(server).length === 0
      ? errorMessage(add.error)
      : null;

  const submit = async () => {
    await add.mutateAsync(picked);
    setPicked([]);
    setOpen(false);
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) add.reset();
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm">
          <PlusIcon /> Add lanes
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Add lanes</DialogTitle>
          <DialogDescription>
            Pick any Surfshark locations. Each location becomes one lane with
            its own exit IP. Surfshark assigns the IP, so you choose the city,
            not the address.
          </DialogDescription>
        </DialogHeader>
        {!provider.isPending && !hasKeys ? (
          <Alert>
            <AlertTitle>Add a Surfshark key first</AlertTitle>
            <AlertDescription>
              <p>Lanes need a WireGuard key to connect.</p>
              <Link
                to="/providers"
                className="underline underline-offset-4"
                onClick={() => setOpen(false)}
              >
                Go to Providers
              </Link>
            </AlertDescription>
          </Alert>
        ) : null}
        <FormError message={general} />
        <FormField
          label="Locations"
          htmlFor="add-lane-locations"
          error={server.locations}
          description={
            locations.isError
              ? `Could not load locations: ${errorMessage(locations.error)}`
              : "New lanes connect one at a time, about 10 seconds apart, so providers don't throttle you."
          }
        >
          <MultiSelect
            id="add-lane-locations"
            options={options}
            value={picked}
            onChange={setPicked}
            placeholder={
              locations.isPending ? "Loading locations…" : "Choose locations"
            }
            searchPlaceholder="Search city, country or ID…"
            emptyText={locations.isPending ? "Loading…" : "No more locations."}
            maxChips={20}
          />
        </FormField>
        <DialogFooter>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button
            onClick={submit}
            disabled={picked.length === 0 || add.isPending}
          >
            {add.isPending ? <Spinner /> : null}
            Add {picked.length || ""} lane{picked.length === 1 ? "" : "s"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
