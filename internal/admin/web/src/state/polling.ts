import { signal, type Signal } from "@preact/signals";

export interface PolledState<T> {
  data: Signal<T | null>;
  error: Signal<string | null>;
  refresh: () => Promise<void>;
  start: () => () => void;
}

export function makePolled<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
): PolledState<T> {
  const data = signal<T | null>(null);
  const error = signal<string | null>(null);

  const refresh = async () => {
    try {
      data.value = await fetcher();
      error.value = null;
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e);
    }
  };

  const start = () => {
    refresh();
    const id = setInterval(refresh, intervalMs);
    return () => clearInterval(id);
  };

  return { data, error, refresh, start };
}
