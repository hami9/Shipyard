import { useCallback, useEffect, useState, type DependencyList } from "react";
import { ApiError } from "./api/client.ts";

export interface Loaded<T> {
  data: T | undefined;
  /** A message for the page; a 401 has already signed the tab out. */
  error: string | undefined;
  loading: boolean;
  reload: () => void;
}

/** useApi runs load when deps change, and drops answers that arrive after a newer request. */
export function useApi<T>(load: () => Promise<T>, deps: DependencyList): Loaded<T> {
  const [state, setState] = useState<{ data?: T; error?: string; loading: boolean }>({ loading: true });
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let current = true;
    setState((s) => ({ ...s, loading: true }));
    load().then(
      (data) => current && setState({ data, loading: false }),
      (err: unknown) => current && setState({ error: message(err), loading: false }),
    );
    return () => {
      current = false;
    };
  }, [...deps, tick]); // load is the caller's closure over deps
  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data: state.data, error: state.error, loading: state.loading, reload };
}

export function message(err: unknown): string {
  if (err instanceof ApiError) {
    return err.status === 404 ? "Not found." : err.message;
  }
  return "The API is not reachable.";
}
