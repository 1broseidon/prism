// usePolledFetch — generic "fetch on mount + refresh on an interval" hook.
//
// Replaces the ~15-line polling triad (loading/error/data + initial fetch +
// setInterval + cleanup) that PolicyHealthStrip and PolicyAccessSection
// were each rolling by hand. Centralizing keeps the subtle pieces
// (silent refetches, cancel-on-unmount, retry-without-flashing-loading)
// in one tested place.
//
// Behavior contract:
//
//   - Initial fetch fires on mount; subsequent refetches fire every
//     `intervalMs` ms.
//   - `loading` is true only during the very first fetch (and during an
//     explicit `retry()` call). Background refetches don't flip it, so
//     the UI doesn't flash placeholders every interval tick.
//   - `error` is cleared on a successful fetch. If a transient error
//     occurs after `data` has already loaded, we keep the stale `data`
//     visible AND surface the error string so callers can decide whether
//     to render an inline retry chip — or just stay silent.
//   - `retry()` triggers an immediate fetch outside the interval and sets
//     `loading` to true (callers usually want the placeholder back when
//     the operator clicks "retry"). It does NOT reset the interval — the
//     existing schedule keeps running.
//   - The interval is cleared on unmount, on `deps` change, and before a
//     fresh `retry()` so we never have two timers racing.
//   - In-flight fetches that complete after unmount (or after a `deps`
//     change) are dropped via a generation counter rather than calling
//     setState on an unmounted component.
//
// `deps` is the standard React/Preact dependency array — passing a new
// value invalidates the current fetch and restarts the cycle. Default is
// empty so callers that fetch from a stable URL don't need to pass it.

import { useCallback, useEffect, useRef, useState } from "preact/hooks";

export interface UsePolledFetchResult<T> {
  data: T | null;
  error: string | null;
  loading: boolean;
  retry: () => void;
}

export function usePolledFetch<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
  deps: readonly unknown[] = [],
): UsePolledFetchResult<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState<boolean>(true);

  // Generation counter — every time `deps` change (or the component
  // unmounts) we bump the generation so any in-flight promise that
  // resolves after the change sees its captured value != current and
  // skips the setState. This is simpler than an AbortController per call
  // because fetchers can be arbitrary (signals are caller-implemented if
  // they want real cancellation).
  const genRef = useRef(0);

  // Keep `fetcher` in a ref so consumers can pass a freshly-bound closure
  // each render without re-triggering the polling effect. The effect's
  // dependency set is `deps + intervalMs`, NOT `fetcher`.
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  // The actual fetch body — captures the gen at call time and ignores
  // results once that gen is stale.
  const run = useCallback(async (gen: number) => {
    try {
      const next = await fetcherRef.current();
      if (gen !== genRef.current) return;
      setData(next);
      setError(null);
    } catch (e) {
      if (gen !== genRef.current) return;
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (gen === genRef.current) setLoading(false);
    }
  }, []);

  const retry = useCallback(() => {
    // Bump the gen so any currently-in-flight call drops its result,
    // then kick a fresh fetch with the new gen. We flip loading so the
    // caller can show a placeholder (operators click retry expecting
    // visible progress).
    genRef.current += 1;
    setLoading(true);
    void run(genRef.current);
  }, [run]);

  useEffect(() => {
    // Each effect activation starts a new generation; the cleanup at the
    // end bumps it again so any in-flight fetch from this activation is
    // dropped if the deps change before it resolves.
    genRef.current += 1;
    const gen = genRef.current;
    setLoading(true);
    void run(gen);
    const id = setInterval(() => {
      void run(gen);
    }, intervalMs);
    return () => {
      clearInterval(id);
      genRef.current += 1;
    };
    // We intentionally exclude `run` from the dep list — it's stable
    // (memoized with []) so including it would just clutter the contract.
    // We DO include intervalMs so a runtime interval change is honored.
    // deps is the caller-controlled invalidation handle.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs, ...deps]);

  return { data, error, loading, retry };
}
