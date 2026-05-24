export type Plan = {
  id: string;
  name: string;
  activePasteLimit: number;
  activeStorageBytes: number;
  singleTextBytes: number;
  singleFileBytes: number;
  singlePasteBytes: number;
  attachmentsPerPasteLimit: number;
  maxRetentionSeconds: number;
  dailyUploadBytes: number;
  dailyShareDownloadBytes: number;
};

export type PlanCatalog = {
  plans: Plan[];
  prices: Price[];
};

export type Price = {
  id: string;
  planId: string;
  period: string;
  amountCents: number;
  currency: string;
  visible: boolean;
  purchaseEnabled: boolean;
  stripeEnabled?: boolean;
  epusdtEnabled?: boolean;
};

export type User = {
  id: string;
  email: string;
  displayName: string;
  language: string;
  role: "user" | "admin";
  emailVerified: boolean;
  planId: string;
  planExpiresAt?: string;
  frozen: boolean;
  createdAt: string;
  deleteRequestedAt?: string;
  deleteScheduledAt?: string;
};

export type Attachment = {
  id: string;
  pasteId: string;
  fileName: string;
  contentType: string;
  size: number;
  sha256: string;
  status: string;
  scanStatus: string;
  risk?: string;
  imagePreview?: {
    width: number;
    height: number;
  };
  downloadCount: number;
  createdAt: string;
};

export type Paste = {
  id: string;
  title: string;
  text: string;
  textPreview: string;
  tags: string[];
  pinned: boolean;
  favorite: boolean;
  status: string;
  scanStatus: string;
  shareCount: number;
  sizeBytes: number;
  expiresAt: string;
  createdAt: string;
  updatedAt: string;
  attachments: Attachment[];
  expired: boolean;
  secondsToLive: number;
};

export type Share = {
  id: string;
  pasteId: string;
  token: string;
  url: string;
  hasPassword: boolean;
  loginRequired: boolean;
  maxVisits: number;
  maxDownloads: number;
  visitCount: number;
  downloadCount: number;
  expiresAt: string;
  revokedAt?: string;
  createdAt: string;
  lastVisitedAt?: string;
  lastDownloadedAt?: string;
};

export type Quota = {
  plan: Plan;
  activePasteCount: number;
  activeStorageBytes: number;
  dailyUploadBytes: number;
  dailyShareDownloadBytes: number;
  overLimit: boolean;
};

export type Order = {
  id: string;
  provider: string;
  planId: string;
  period: string;
  amountCents: number;
  currency: string;
  status: string;
  checkoutUrl?: string;
  address?: string;
  chain?: string;
  txId?: string;
  createdAt: string;
  expiresAt?: string;
  paidAt?: string;
};

export type WebhookEvent = {
  id: string;
  provider: string;
  eventType: string;
  targetId: string;
  processed: boolean;
  metadata?: Record<string, unknown>;
  receivedAt: string;
};

export type AuditLog = {
  id: string;
  actorId: string;
  action: string;
  target: string;
  metadata?: Record<string, unknown>;
  createdAt: string;
};

export type AdminAttachment = Attachment & {
  userId: string;
  pasteTitle: string;
};

export type AdminShare = Share & {
  userId: string;
};

export type AdminQueues = {
  cleanupJobs: QueueItem[];
  cleanupFailures: QueueItem[];
  scanJobs: QueueItem[];
  scanFailures: QueueItem[];
  failedJobs: QueueItem[];
  reports: Report[];
};

export type QueueItem = {
  id: string;
  kind: string;
  targetId: string;
  status: string;
  error?: string;
  attempts: number;
  runAfter: string;
  createdAt: string;
  updatedAt: string;
};

export type Report = {
  id: string;
  userId?: string;
  target: string;
  reason: string;
  status: string;
  createdAt: string;
};

export type ApiError = Error & {
  status?: number;
  code?: string;
};

export type AuthResult = {
  user: User;
  sessionExpiresAt: string;
  devEmailVerificationToken?: string;
};

let csrfToken: string | null = null;

function requiresCsrf(init: RequestInit): boolean {
  const method = (init.method ?? "GET").toUpperCase();
  return !["GET", "HEAD", "OPTIONS"].includes(method);
}

async function fetchCsrfToken(): Promise<string> {
  const response = await fetch("/api/v1/csrf", {
    headers: { Accept: "application/json" },
    credentials: "include",
  });
  if (!response.ok) {
    throw new Error("failed to initialize request protection");
  }
  const payload = (await response.json()) as { csrfToken: string };
  csrfToken = payload.csrfToken;
  return payload.csrfToken;
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  const isForm = init.body instanceof FormData;

  if (!isForm && init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  headers.set("Accept", "application/json");
  if (requiresCsrf(init) && !headers.has("X-CSRF-Token")) {
    headers.set("X-CSRF-Token", csrfToken ?? (await fetchCsrfToken()));
  }

  let response = await fetch(`/api/v1${path}`, {
    ...init,
    headers,
    credentials: "include",
  });

  if (response.status === 403 && requiresCsrf(init)) {
    const clone = response.clone();
    try {
      const payload = (await clone.json()) as { error?: string };
      if (payload.error === "csrf_required") {
        headers.set("X-CSRF-Token", await fetchCsrfToken());
        response = await fetch(`/api/v1${path}`, {
          ...init,
          headers,
          credentials: "include",
        });
      }
    } catch {
      // Fall through to the normal error handler below.
    }
  }

  if (!response.ok) {
    let message = response.statusText;
    let code = "request_failed";
    try {
      const payload = (await response.json()) as {
        error?: string;
        message?: string;
      };
      message = payload.message ?? message;
      code = payload.error ?? code;
    } catch {
      // Keep the HTTP status text.
    }
    const error = new Error(message) as ApiError;
    error.status = response.status;
    error.code = code;
    throw error;
  }

  if (response.status === 204) {
    return undefined as T;
  }

  return (await response.json()) as T;
}

export const client = {
  plans: () => api<PlanCatalog>("/plans"),
  me: () => api<User>("/me"),
  register: (body: { email: string; password: string; displayName: string }) =>
    api<AuthResult>("/auth/register", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  login: (body: { email: string; password: string }) =>
    api<AuthResult>("/auth/login", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  googleOAuthStartPath: (returnTo = "/") =>
    `/api/v1/auth/google/start?${new URLSearchParams({ returnTo }).toString()}`,
  logout: () => api<{ status: string }>("/auth/logout", { method: "POST" }),
  logoutAll: () =>
    api<{ status: string }>("/auth/logout-all", { method: "POST" }),
  startEmailVerification: () =>
    api<{ devToken?: string; message: string }>("/auth/email-verification/start", {
      method: "POST",
    }),
  finishEmailVerification: (token: string) =>
    api<User>("/auth/email-verification/finish", {
      method: "POST",
      body: JSON.stringify({ token }),
    }),
  startMagic: (email: string) =>
    api<{ devToken?: string; message: string }>("/auth/magic/start", {
      method: "POST",
      body: JSON.stringify({ email }),
    }),
  finishMagic: (token: string) =>
    api<AuthResult>("/auth/magic/finish", {
      method: "POST",
      body: JSON.stringify({ token }),
    }),
  passwordReset: (email: string) =>
    api<{ devToken?: string; message: string }>("/auth/password-reset/start", {
      method: "POST",
      body: JSON.stringify({ email }),
    }),
  finishPasswordReset: (token: string, password: string) =>
    api<{ status: string }>("/auth/password-reset/finish", {
      method: "POST",
      body: JSON.stringify({ token, password }),
    }),
  updateMe: (body: { displayName: string; language: string }) =>
    api<User>("/me", {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  quota: () => api<Quota>("/quota"),
  pastes: (params: URLSearchParams) =>
    api<{ pastes: Paste[] }>(`/pastes?${params.toString()}`),
  createPaste: (body: {
    title: string;
    text: string;
    tags: string[];
    pinned: boolean;
    favorite: boolean;
    expiresInSeconds: number;
  }) =>
    api<Paste>("/pastes", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  updatePaste: (
    id: string,
    body: Partial<
      Pick<Paste, "title" | "text" | "tags" | "pinned" | "favorite">
    >,
  ) =>
    api<Paste>(`/pastes/${id}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  extendPaste: (id: string, expiresInSeconds: number) =>
    api<Paste>(`/pastes/${id}/extend`, {
      method: "POST",
      body: JSON.stringify({ expiresInSeconds }),
    }),
  deletePaste: (id: string) =>
    api<{ status: string }>(`/pastes/${id}`, { method: "DELETE" }),
  uploadAttachment: (pasteId: string, file: File) => {
    const form = new FormData();
    form.append("file", file);
    return api<Attachment>(`/pastes/${pasteId}/attachments`, {
      method: "POST",
      body: form,
    });
  },
  shares: () => api<{ shares: Share[] }>("/shares"),
  createShare: (
    pasteId: string,
    body: {
      password: string;
      loginRequired: boolean;
      maxVisits: number;
      maxDownloads: number;
      expiresInSeconds: number;
    },
  ) =>
    api<Share>(`/pastes/${pasteId}/shares`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  revokeShare: (id: string) =>
    api<{ status: string }>(`/shares/${id}`, { method: "DELETE" }),
  accessShare: (token: string, password: string) =>
    api<{ paste: Paste; share: Share }>(`/shares/${token}/access`, {
      method: "POST",
      body: JSON.stringify({ password }),
    }),
  prices: () => api<PlanCatalog>("/billing/prices"),
  orders: () => api<{ orders: Order[] }>("/billing/orders"),
  createOrder: (body: { provider: string; planId: string; period: string }) =>
    api<Order>("/billing/orders", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  processBillingWebhook: (provider: string, body: {
    eventType: string;
    orderId: string;
    txId: string;
    idempotencyKey: string;
    metadata?: Record<string, unknown>;
  }) =>
    api<{ webhookEvent: WebhookEvent; order: Order | null }>(
      `/billing/webhooks/${encodeURIComponent(provider)}`,
      {
        method: "POST",
        body: JSON.stringify(body),
      },
    ),
  report: (body: { target: string; reason: string }) =>
    api<Report>("/reports", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  exportMe: () => api<Record<string, unknown>>("/me/export"),
  requestDelete: () => api<User>("/me/delete-request", { method: "POST" }),
  cancelDelete: () => api<User>("/me/delete-cancel", { method: "POST" }),
  executeDelete: () =>
    api<{ status: string }>("/me/delete-now", { method: "POST" }),
  adminDashboard: () => api<Record<string, unknown>>("/admin/dashboard"),
  adminUsers: () => api<{ users: User[] }>("/admin/users"),
  adminPastes: () => api<{ pastes: Paste[] }>("/admin/pastes"),
  adminAttachments: (query: string) =>
    api<{ attachments: AdminAttachment[] }>(
      `/admin/attachments?${new URLSearchParams({ query }).toString()}`,
    ),
  adminFreezeAttachment: (id: string, frozen: boolean) =>
    api<Attachment>(`/admin/attachments/${id}/freeze`, {
      method: "PATCH",
      body: JSON.stringify({ frozen }),
    }),
  adminRetryScan: (id: string) =>
    api<Attachment>(`/admin/attachments/${id}/retry-scan`, { method: "POST" }),
  adminShares: () => api<{ shares: AdminShare[] }>("/admin/shares"),
  adminRevokeShare: (id: string) =>
    api<Share>(`/admin/shares/${id}/revoke`, { method: "POST" }),
  adminOrders: () => api<{ orders: Order[] }>("/admin/orders"),
  adminMarkOrderPaid: (id: string, txId: string) =>
    api<Order>(`/admin/orders/${id}/mark-paid`, {
      method: "POST",
      body: JSON.stringify({ txId }),
    }),
  adminWebhookEvents: () =>
    api<{ webhookEvents: WebhookEvent[] }>("/admin/webhook-events"),
  adminReplayWebhookEvent: (id: string) =>
    api<WebhookEvent>(`/admin/webhook-events/${id}/replay`, {
      method: "POST",
    }),
  adminQueues: () => api<AdminQueues>("/admin/queues"),
  adminResolveReport: (id: string, status: "open" | "resolved" | "dismissed") =>
    api<Report>(`/admin/reports/${id}/status`, {
      method: "POST",
      body: JSON.stringify({ status }),
    }),
  adminAuditLogs: () => api<{ auditLogs: AuditLog[] }>("/admin/audit-logs"),
  runCleanup: () =>
    api<Record<string, number>>("/admin/cleanup/run", { method: "POST" }),
};

export function attachmentDownloadPath(id: string): string {
  return `/api/v1/attachments/${encodeURIComponent(id)}/download`;
}

export function sharedAttachmentDownloadPath(
  token: string,
  attachmentID: string,
  password: string,
): string {
  const params = new URLSearchParams();
  if (password) {
    params.set("password", password);
  }
  const query = params.toString();
  return `/api/v1/shares/${encodeURIComponent(token)}/attachments/${encodeURIComponent(attachmentID)}/download${query ? `?${query}` : ""}`;
}

export function formatBytes(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unitIndex = 0;

  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }

  return `${Number.isInteger(value) ? value : value.toFixed(1)} ${units[unitIndex]}`;
}

export function formatDuration(seconds: number): string {
  const days = seconds / 86400;
  if (days >= 1) {
    return `${Math.round(days)}d`;
  }
  return `${Math.max(0, Math.round(seconds / 3600))}h`;
}
