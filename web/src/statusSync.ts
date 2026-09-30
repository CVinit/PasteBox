import { useCallback, useEffect, useRef, useState } from "react";

// The status channel is a server-sent event subscription: the server pushes an
// authoritative snapshot as soon as it changes, and the browser owns the
// transport state. That state is what the pages show — a dropped connection and
// a page with nothing new to show must not look the same.
//
// The channel is only a change signal for the account workspace, but it is the
// authority for one send's claim state, so every snapshot replaces the previous
// one rather than being merged into it.

export type SyncConnection =
  | "idle"
  | "connecting"
  | "connected"
  | "reconnecting"
  | "disconnected"
  // The server is at its connection bound for this scope. It is a capacity
  // answer rather than a lost connection, so the page names it and keeps
  // retrying: the next attempt can succeed once another page closes.
  | "limited"
  | "ended";

// disconnectedAfterMs is how long a channel may stay down before the page stops
// saying "reconnecting" and says the connection is gone. It is a bound on the
// waiting, not on the retrying: the channel keeps trying after that.
const disconnectedAfterMs = 12_000;
const retryBaseMs = 1_000;
const retryMaxMs = 15_000;

type StatusState<T> = {
  path: string | null;
  snapshot: T | null;
  receivedAt: number;
};

export type StatusStream<T> = {
  connection: SyncConnection;
  snapshot: T | null;
  // receivedAt is when the last snapshot arrived, so a surface can say how old
  // what it shows is.
  receivedAt: number;
  // reconnect closes the current subscription and opens a new one, which is how
  // a page picks up a credential it earned after the channel was opened.
  reconnect: () => void;
};

// useStatusStream subscribes to one status channel. A null path means there is
// nothing to watch, which is how a page turns the channel off instead of
// watching a scope it does not own.
export function useStatusStream<T>(path: string | null): StatusStream<T> {
  const [connection, setConnection] = useState<SyncConnection>(
    path ? "connecting" : "idle",
  );
  const [state, setState] = useState<StatusState<T>>({
    path: null,
    snapshot: null,
    receivedAt: 0,
  });
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    if (!path) {
      setConnection("idle");
      return;
    }
    if (typeof EventSource === "undefined") {
      setConnection("disconnected");
      return;
    }

    let disposed = false;
    let source: EventSource | null = null;
    let retryTimer = 0;
    let downTimer = 0;
    let attempts = 0;
    let openedOnce = false;

    const clearTimers = () => {
      window.clearTimeout(retryTimer);
      window.clearTimeout(downTimer);
    };

    const setIfLive = (next: SyncConnection) => {
      if (!disposed) setConnection(next);
    };

    // markDown starts the one countdown that separates "reconnecting" from
    // "connection lost". It is armed once per outage rather than on every retry,
    // because a retry that keeps failing is still the same outage.
    const markDown = () => {
      if (disposed || downTimer) return;
      downTimer = window.setTimeout(() => {
        downTimer = 0;
        if (!disposed) setConnection("disconnected");
      }, disconnectedAfterMs);
    };

    const markUp = () => {
      window.clearTimeout(downTimer);
      downTimer = 0;
    };

    const scheduleRetry = () => {
      if (disposed) return;
      const delay = Math.min(retryBaseMs * 2 ** attempts, retryMaxMs);
      attempts += 1;
      retryTimer = window.setTimeout(() => {
        if (!disposed) open();
      }, delay);
    };

    const open = () => {
      if (disposed) return;
      source = new EventSource(path, { withCredentials: true });
      setIfLive(openedOnce ? "reconnecting" : "connecting");
      markDown();

      source.addEventListener("open", () => {
        if (disposed) return;
        attempts = 0;
        openedOnce = true;
        markUp();
        setConnection("connected");
      });
      source.addEventListener("status", (event) => {
        if (disposed) return;
        const parsed = parseSnapshot<T>((event as MessageEvent<string>).data);
        if (parsed === null) return;
        setState({ path, snapshot: parsed, receivedAt: Date.now() });
        setConnection("connected");
      });
      source.addEventListener("closed", (event) => {
        if (disposed) return;
        markUp();
        source?.close();
        if (closedReason(event) === "stream_limit") {
          // The bound is per scope, so another page closing frees a slot. The
          // page waits it out instead of claiming the connection is gone.
          setConnection("limited");
          scheduleRetry();
          return;
        }
        // The server ended the subscription on purpose, for example because the
        // page grant lapsed. Retrying would only fail again, so the page says
        // the channel is over instead of looping.
        setConnection("ended");
      });
      source.addEventListener("error", () => {
        if (disposed) return;
        markDown();
        if (source?.readyState === EventSource.CLOSED) {
          // A refused or unstreamable response closes the stream for good; the
          // browser will not retry it, so this hook does, with backoff.
          source?.close();
          setConnection((current) =>
            current === "connected" ? "reconnecting" : current,
          );
          scheduleRetry();
          return;
        }
        setConnection((current) =>
          current === "connected" ? "reconnecting" : current,
        );
      });
    };

    // Coming back to a hidden tab, or back online, is a reason to try now
    // rather than to wait out a backoff. Neither is used as the connection
    // state itself: only a server round trip decides that.
    const retryNow = () => {
      if (disposed) return;
      if (source?.readyState === EventSource.OPEN) return;
      attempts = 0;
      window.clearTimeout(retryTimer);
      source?.close();
      open();
    };
    const onVisibility = () => {
      if (document.visibilityState === "visible") retryNow();
    };

    open();
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("online", retryNow);
    return () => {
      disposed = true;
      clearTimers();
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("online", retryNow);
      source?.close();
    };
  }, [path, generation]);

  const reconnect = useCallback(() => setGeneration((value) => value + 1), []);

  // A snapshot only belongs to the path it arrived from, so a page that switches
  // scopes never renders the previous scope's state.
  const owned = state.path === path ? state : null;
  return {
    connection,
    snapshot: owned?.snapshot ?? null,
    receivedAt: owned?.receivedAt ?? 0,
    reconnect,
  };
}

// closedReason reads why the server ended a subscription. An unreadable reason
// is treated as the terminal one, because retrying a refusal is what the reason
// exists to avoid.
function closedReason(event: Event): string {
  try {
    const parsed = JSON.parse((event as MessageEvent<string>).data) as {
      reason?: string;
    };
    return parsed.reason ?? "";
  } catch {
    return "";
  }
}

// parseSnapshot decodes one status payload. A malformed event is ignored rather
// than rendered: the next snapshot will arrive from the same server that sent
// the broken one, and a page must not crash on it.
function parseSnapshot<T>(data: string): T | null {
  try {
    return JSON.parse(data) as T;
  } catch {
    return null;
  }
}
