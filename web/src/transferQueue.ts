import { useCallback, useMemo, useRef, useState } from "react";
import { isAbortError, type TransferItemInput } from "./api";

// One send is a queue of files that share a single transfer. The queue declares
// its manifest up front, uploads the files one at a time with real byte
// progress, and only publishes the share once every declared file finished
// uploading. A failed file stays retryable on its own; finished files are never
// uploaded twice.

export type TransferQueueItemStatus =
  | "staged"
  | "uploading"
  | "uploaded"
  | "failed";

export type TransferQueueItem = {
  id: string;
  file: File;
  status: TransferQueueItemStatus;
  loaded: number;
  total: number;
  message: string;
};

export type TransferQueuePhase =
  | "staged"
  | "uploading"
  | "publishing"
  | "published"
  | "failed";

export type TransferQueueShare = {
  url: string;
  expiresAt: string;
  // pickupCode lets the success page offer the short code next to the link.
  pickupCode?: string;
  // pasteId lets an account surface the record a finished send created.
  pasteId?: string;
  // shareToken is the share's own credential. The success page watches its send
  // over the share's status channel, which is the same channel the recipient
  // uses, so the token has to travel with the result.
  shareToken?: string;
  // claimQuota is how many anonymous claims the published send grants, so the
  // success page can state the count the sender chose.
  claimQuota?: number;
  // burnAfterReading tells the success page that this send destroys itself, so
  // the promise is stated where the link is handed over.
  burnAfterReading?: boolean;
};

// TransferQueueAdapter hides who is sending: an account session or a guest
// token. The queue only needs one way to create, upload, publish and cancel.
export type TransferQueueAdapter = {
  create: (
    manifest: TransferItemInput[],
    idempotencyKey: string,
  ) => Promise<string>;
  upload: (
    transferId: string,
    itemId: string,
    file: File,
    onProgress: (loaded: number, total: number) => void,
    signal: AbortSignal,
  ) => Promise<void>;
  publish: (transferId: string) => Promise<TransferQueueShare>;
  cancel: (transferId: string) => Promise<void>;
};

function newClientId(prefix: string): string {
  const random =
    globalThis.crypto?.randomUUID?.() ??
    `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `${prefix}-${random}`;
}

function stagedItem(file: File): TransferQueueItem {
  return {
    id: newClientId("itm"),
    file,
    status: "staged",
    loaded: 0,
    total: file.size,
    message: "",
  };
}

function messageFor(error: unknown): string {
  const apiError = error as { message?: string } | null;
  return apiError?.message || "upload_failed";
}

export function useTransferQueue(adapter: TransferQueueAdapter) {
  const [items, setItems] = useState<TransferQueueItem[]>([]);
  const [phase, setPhase] = useState<TransferQueuePhase>("staged");
  const [share, setShare] = useState<TransferQueueShare | null>(null);
  const [error, setError] = useState("");

  // The queue is driven by async code, so the ref is the authoritative copy and
  // the state above is only what React renders.
  const itemsRef = useRef<TransferQueueItem[]>([]);
  // phase is mirrored for the same reason: a handler that resets a queue and
  // stages into it in one tick must not read the phase it just replaced.
  const phaseRef = useRef<TransferQueuePhase>("staged");
  const transferRef = useRef("");
  const keyRef = useRef<{ signature: string; key: string } | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  // Bumped by cancel/reset so an in-flight run can tell it was abandoned.
  const runRef = useRef(0);

  const commit = useCallback((next: TransferQueueItem[]) => {
    itemsRef.current = next;
    setItems(next);
  }, []);

  const commitPhase = useCallback((next: TransferQueuePhase) => {
    phaseRef.current = next;
    setPhase(next);
  }, []);

  const patchItem = useCallback(
    (id: string, patch: Partial<TransferQueueItem>) => {
      commit(
        itemsRef.current.map((item) =>
          item.id === id ? { ...item, ...patch } : item,
        ),
      );
    },
    [commit],
  );

  const resetItemProgress = useCallback(() => {
    commit(
      itemsRef.current.map((item) => ({
        ...item,
        status: "staged" as const,
        loaded: 0,
        total: item.file.size,
        message: "",
      })),
    );
  }, [commit]);

  const addFiles = useCallback(
    (files: File[]): boolean => {
      if (files.length === 0) return false;
      // The manifest is fixed once a transfer exists, so files can only join a
      // send that has not started yet.
      if (phaseRef.current !== "staged") return false;
      commit([...itemsRef.current, ...files.map(stagedItem)]);
      return true;
    },
    [commit],
  );

  const removeFile = useCallback(
    (id: string) => {
      if (phaseRef.current !== "staged") return;
      commit(itemsRef.current.filter((item) => item.id !== id));
    },
    [commit],
  );

  const idempotencyKeyFor = useCallback((ids: string[]): string => {
    const signature = ids.join("\u0000");
    if (!keyRef.current || keyRef.current.signature !== signature) {
      keyRef.current = { signature, key: newClientId("send") };
    }
    return keyRef.current.key;
  }, []);

  const runUpload = useCallback(
    async (pendingIds: string[]) => {
      if (phaseRef.current === "uploading" || phaseRef.current === "publishing")
        return;
      const runId = runRef.current;
      const abandoned = () => runRef.current !== runId;
      const controller = new AbortController();
      abortRef.current = controller;
      setError("");
      commitPhase("uploading");
      let transferId = transferRef.current;
      try {
        if (!transferId) {
          if (itemsRef.current.length === 0) {
            commitPhase("staged");
            return;
          }
          const manifest: TransferItemInput[] = itemsRef.current.map(
            (item) => ({
              itemId: item.id,
              fileName: item.file.name,
              contentType: item.file.type,
              size: item.file.size,
            }),
          );
          transferId = await adapter.create(
            manifest,
            idempotencyKeyFor(itemsRef.current.map((item) => item.id)),
          );
          if (abandoned() || controller.signal.aborted) {
            await discard(adapter, transferId);
            return;
          }
          transferRef.current = transferId;
        }
        if (abandoned() || controller.signal.aborted) {
          await discard(adapter, transferId);
          return;
        }

        for (const id of pendingIds) {
          if (abandoned() || controller.signal.aborted) {
            await discard(adapter, transferId);
            return;
          }
          const item = itemsRef.current.find((entry) => entry.id === id);
          if (!item || item.status === "uploaded") continue;
          patchItem(id, {
            status: "uploading",
            loaded: 0,
            total: item.file.size,
            message: "",
          });
          try {
            await adapter.upload(
              transferId,
              id,
              item.file,
              (loaded, total) => {
                if (!abandoned() && !controller.signal.aborted) {
                  patchItem(id, { loaded, total });
                }
              },
              controller.signal,
            );
          } catch (uploadError) {
            if (
              abandoned() ||
              controller.signal.aborted ||
              isAbortError(uploadError)
            ) {
              await discard(adapter, transferId);
              return;
            }
            // Only this file failed; its siblings stay uploaded so a retry does
            // not re-send them.
            patchItem(id, {
              status: "failed",
              message: messageFor(uploadError),
            });
            continue;
          }
          if (abandoned() || controller.signal.aborted) {
            await discard(adapter, transferId);
            return;
          }
          patchItem(id, {
            status: "uploaded",
            loaded: item.file.size,
            total: item.file.size,
            message: "",
          });
        }

        if (abandoned() || controller.signal.aborted) {
          await discard(adapter, transferId);
          return;
        }
        if (!itemsRef.current.every((item) => item.status === "uploaded")) {
          commitPhase("failed");
          return;
        }
        commitPhase("publishing");
        const published = await adapter.publish(transferId);
        if (abandoned()) {
          await discard(adapter, transferId);
          return;
        }
        setShare(published);
        commitPhase("published");
      } catch (runError) {
        if (abandoned() || controller.signal.aborted || isAbortError(runError))
          return;
        setError(messageFor(runError));
        commitPhase("failed");
      } finally {
        if (abortRef.current === controller) abortRef.current = null;
      }
    },
    [adapter, commitPhase, idempotencyKeyFor, patchItem],
  );

  const start = useCallback(() => {
    void runUpload(itemsRef.current.map((item) => item.id));
  }, [runUpload]);

  // Retry re-runs only what is still missing: a failed file, or the publish
  // step when every file already finished.
  const retry = useCallback(() => {
    const pending = itemsRef.current
      .filter((item) => item.status !== "uploaded")
      .map((item) => item.id);
    void runUpload(
      transferRef.current ? pending : itemsRef.current.map((item) => item.id),
    );
  }, [runUpload]);

  // cancel abandons the send and reports whether the server confirmed it, so
  // the caller can tell the user the truth about the draft.
  const cancel = useCallback(async (): Promise<boolean> => {
    runRef.current += 1;
    abortRef.current?.abort();
    abortRef.current = null;
    const transferId = transferRef.current;
    transferRef.current = "";
    keyRef.current = null;
    resetItemProgress();
    commitPhase("staged");
    setShare(null);
    setError("");
    if (!transferId) return true;
    try {
      await adapter.cancel(transferId);
      return true;
    } catch {
      // The unpublished draft still expires with the retention window.
      return false;
    }
  }, [adapter, commitPhase, resetItemProgress]);

  const reset = useCallback(() => {
    runRef.current += 1;
    abortRef.current?.abort();
    abortRef.current = null;
    transferRef.current = "";
    keyRef.current = null;
    commit([]);
    commitPhase("staged");
    setShare(null);
    setError("");
  }, [commit, commitPhase]);

  const counts = useMemo(() => {
    let uploaded = 0;
    let failed = 0;
    for (const item of items) {
      if (item.status === "uploaded") uploaded += 1;
      if (item.status === "failed") failed += 1;
    }
    return { uploaded, failed, total: items.length };
  }, [items]);

  return {
    items,
    phase,
    share,
    error,
    counts,
    canStage: phase === "staged",
    canSend: phase === "staged" && items.length > 0,
    addFiles,
    removeFile,
    start,
    retry,
    cancel,
    reset,
  };
}

// discard drops a draft the user walked away from. Cleanup is best effort: the
// send expires on its own if the call fails.
async function discard(adapter: TransferQueueAdapter, transferId: string) {
  try {
    await adapter.cancel(transferId);
  } catch {
    // The draft still expires with the retention window.
  }
}

export type TransferQueue = ReturnType<typeof useTransferQueue>;

// A text send has no files to upload: it creates the transfer and publishes it
// in one go. Retrying an unchanged draft reuses the same idempotency key and
// transfer, so a failed publish cannot mint a second send.
export type TextSendAdapter = {
  create: (idempotencyKey: string) => Promise<string>;
  publish: (transferId: string) => Promise<TransferQueueShare>;
};

export type TextSendPhase = "idle" | "sending" | "sent" | "failed";

export function useTextSend(adapter: TextSendAdapter) {
  const [phase, setPhase] = useState<TextSendPhase>("idle");
  const [share, setShare] = useState<TransferQueueShare | null>(null);
  const [error, setError] = useState("");
  const draftRef = useRef<{
    signature: string;
    key: string;
    transferId: string;
  } | null>(null);
  const runRef = useRef(0);

  const send = useCallback(
    async (signature: string) => {
      // Editing the draft starts a new send; retrying an unchanged draft keeps
      // the key, so the server returns the transfer it already created.
      if (!draftRef.current || draftRef.current.signature !== signature) {
        draftRef.current = {
          signature,
          key: newClientId("text"),
          transferId: "",
        };
      }
      const draft = draftRef.current;
      const runId = ++runRef.current;
      setError("");
      setPhase("sending");
      try {
        if (!draft.transferId) {
          draft.transferId = await adapter.create(draft.key);
        }
        const published = await adapter.publish(draft.transferId);
        if (runRef.current !== runId) return;
        setShare(published);
        setPhase("sent");
      } catch (sendError) {
        if (runRef.current !== runId) return;
        setError(messageFor(sendError));
        setPhase("failed");
      }
    },
    [adapter],
  );

  const reset = useCallback(() => {
    runRef.current += 1;
    draftRef.current = null;
    setShare(null);
    setError("");
    setPhase("idle");
  }, []);

  return { phase, share, error, send, reset };
}
