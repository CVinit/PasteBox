export type Plan = {
  id: string;
  name: string;
  activePasteLimit: number;
  activeStorageBytes: number;
  singleTextBytes: number;
  singleFileBytes: number;
  singlePasteBytes: number;
  attachmentsPerPasteLimit: number;
  tagsPerPasteLimit: number;
  maxRetentionSeconds: number;
  dailyUploadBytes: number;
  dailyShareDownloadBytes: number;
};

export type PlanCatalog = {
  plans: Plan[];
  prices: Price[];
  guestUploads?: GuestUploadConfig;
  registration?: RegistrationConfig;
  // transfers carries the send bounds the server enforces, so the send form
  // derives them instead of copying the numbers.
  transfers?: TransferConfig;
};

export type TransferConfig = {
  maxClaimQuota: number;
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

export type OrderStatus =
  | "pending"
  | "paid"
  | "failed"
  | "expired"
  | "canceled"
  | "refunded"
  | "needs_review"
  | (string & {});

export type User = {
  id: string;
  email: string;
  displayName: string;
  language: string;
  role: "user" | "admin";
  emailVerified: boolean;
  planId: string;
  planExpiresAt?: string;
  oauthProviders: string[];
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

export type TransferItem = {
  itemId: string;
  fileName: string;
  contentType: string;
  size: number;
  status: string;
  attachmentId?: string;
  scanStatus?: string;
};

export type Transfer = {
  id: string;
  status: string;
  pasteId: string;
  items: TransferItem[];
  share?: Share;
  // pickupCode is only sent to the sender: the success page shows it so a
  // recipient can type 6 characters instead of pasting a long link.
  pickupCode?: string;
  // claimQuota is how many anonymous claims the send grants; claimedCount is
  // how many have been spent. Both are only reported to the sender.
  claimQuota: number;
  claimedCount: number;
  // burnAfterReading is the destructive switch the sender chose. destroyedAt
  // and destroyReason report the terminal state, and cleanupStatus says how far
  // the background release of the bytes has got.
  burnAfterReading: boolean;
  destroyedAt?: string;
  destroyReason?: string;
  cleanupStatus?: string;
  expiresAt: string;
  createdAt: string;
  updatedAt: string;
  publishedAt?: string;
  canceledAt?: string;
};

// TransferAccess is the claim state a recipient sees for a transfer-backed
// share. It carries no content: a text body only arrives once the recipient
// holds a live claim.
export type TransferAccess = {
  id: string;
  kind: string;
  claimQuota: number;
  claimedCount: number;
  claimsRemaining: number;
  claimed: boolean;
  claimId?: string;
  claimExpiresAt?: string;
  expiresAt: string;
};

// TransferClaim is one anonymous claim: a file claim opens a bounded session
// that covers the whole batch, a text claim hands out the body once.
export type TransferClaim = {
  id: string;
  kind: string;
  status: string;
  expiresAt: string;
  completedAt?: string;
  createdAt: string;
};

export type TransferClaimResult = {
  claim: TransferClaim;
  claimToken?: string;
  text?: string;
};

// TransferStatusSnapshot is the credential-scoped state one send reports over
// its status channel. It carries no file names, no attachment identifiers and
// no content, so watching a send can never reveal more than the page already
// holds.
export type TransferStatusSnapshot = {
  transferId: string;
  // state is the one word a surface renders: draft, canceled, claimable,
  // claimed, exhausted, expired, revoked or destroyed.
  state: string;
  kind?: string;
  burnAfterReading: boolean;
  claimQuota: number;
  claimedCount: number;
  claimsRemaining: number;
  claimed: boolean;
  claimId?: string;
  claimExpiresAt?: string;
  expiresAt: string;
  destroyedAt?: string;
  destroyReason?: string;
  cleanupStatus?: string;
};

// TransferRecord is one row of the sender's own send records: the shared status
// plus the link, the pickup code and how many files the send declares.
export type TransferRecord = {
  transferId: string;
  title?: string;
  state: string;
  burnAfterReading: boolean;
  claimQuota: number;
  claimedCount: number;
  claimsRemaining: number;
  itemCount: number;
  shareToken?: string;
  shareUrl?: string;
  pickupCode?: string;
  expiresAt: string;
  createdAt: string;
  updatedAt: string;
  publishedAt?: string;
  canceledAt?: string;
  destroyedAt?: string;
  destroyReason?: string;
  cleanupStatus?: string;
};

// PasteStatusMarker is the change marker of one record. It carries no body: a
// surface that sees it change re-reads the record through the content API.
export type PasteStatusMarker = {
  id: string;
  status: string;
  updatedAt: string;
};

// AccountStatusSnapshot is the whole account snapshot behind the account status
// channel.
export type AccountStatusSnapshot = {
  transfers: TransferRecord[];
  pastes: PasteStatusMarker[];
};

export type TransferItemInput = {
  itemId: string;
  fileName: string;
  contentType?: string;
  size?: number;
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
  status: OrderStatus;
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
  queuedMails: MailQueueItem[];
  failedMails: MailQueueItem[];
  reports: Report[];
};

export type MailQueueItem = {
  id: string;
  to: string;
  subject: string;
  status: string;
  attempts: number;
  lastError?: string;
  runAfter: string;
  createdAt: string;
  sentAt?: string;
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

export type GuestUploadConfig = {
  enabled: boolean;
  requireTurnstile: boolean;
  retentionSeconds: number;
  activePasteLimit: number;
  activeStorageBytes: number;
  singleTextBytes: number;
  singleFileBytes: number;
  singlePasteBytes: number;
  attachmentsPerPasteLimit: number;
  dailyUploadBytes: number;
  dailyShareDownloadBytes: number;
  shareDownloadsEnabled: boolean;
};

export type RegistrationConfig = {
  allowedDomains: string[];
  requireEmailVerification: boolean;
  requireTurnstile: boolean;
  turnstileSiteKey?: string;
};

export type RuntimeRateLimitConfig = {
  enabled: boolean;
  windowSeconds: number;
  emailVerificationLimit: number;
  registerLimit: number;
  loginLimit: number;
  writeLimit: number;
  uploadLimit: number;
  shareCreateLimit: number;
  shareAccessLimit: number;
  downloadLimit: number;
  webhookLimit: number;
};

export type AlertConfig = {
  enabled: boolean;
  telegramEnabled: boolean;
  silent: boolean;
  cooldownSeconds: number;
  cpuPercentThreshold: number;
  memoryPercentThreshold: number;
  diskPercentThreshold: number;
  objectStorageBytesThreshold: number;
  scanFailureDepthThreshold: number;
  failedJobDepthThreshold: number;
  mailFailedDepthThreshold: number;
  reportsOpenThreshold: number;
};

export type ProviderConfigStatus = {
  provider: string;
  configured: boolean;
  secretManaged: boolean;
  requiredEnv: string[];
  missingEnv: string[];
  nonSensitive?: Record<string, string>;
  lastTestStatus?: string;
  lastTestMessage?: string;
};

export type ProviderStatus = {
  mailer: ProviderConfigStatus;
  google: ProviderConfigStatus;
  github: ProviderConfigStatus;
  turnstile: ProviderConfigStatus;
  telegram: ProviderConfigStatus;
  s3: ProviderConfigStatus;
  stripe: ProviderConfigStatus;
  epusdt: ProviderConfigStatus;
};

export type ManagedConfig = {
  version: number;
  site: {
    appName: string;
    publicUrl: string;
    supportEmail: string;
    abuseEmail: string;
    corsAllowedOrigins: string[];
  };
  workerHeartbeatMaxAgeSeconds: number;
  s3: {
    endpoint: string;
    bucket: string;
    region: string;
    usePathStyle: boolean;
  };
  scanner: {
    provider: string;
    clamav: { addr: string; timeout: number };
  };
  googleOAuth: { clientId: string; redirectUrl: string };
  githubOAuth: { clientId: string; redirectUrl: string };
  turnstile: { siteKey: string; verifyUrl: string };
  telegram: { chatId: string; apiBaseUrl: string };
  mailerProvider: string;
  smtp: {
    host: string;
    port: number;
    username: string;
    fromEmail: string;
    fromName: string;
    tlsMode: string;
  };
  devAuthTokens: boolean;
  stripeEnabled: boolean;
  epusdtEnabled: boolean;
  stripe: { checkoutUrlTemplate: string };
  epusdt: {
    pid: string;
    checkoutUrlTemplate: string;
    address: string;
    chain: string;
  };
};

export type ManagedSecretStatus = {
  s3AccessKey: boolean;
  s3SecretKey: boolean;
  googleClientSecret: boolean;
  githubClientSecret: boolean;
  turnstileSecretKey: boolean;
  telegramBotToken: boolean;
  smtpPassword: boolean;
  stripeWebhookSecret: boolean;
  epusdtSecretKey: boolean;
};

export type ManagedSecretPatch = Partial<
  Record<keyof ManagedSecretStatus, string>
>;

export type ManagedConfigView = {
  config: ManagedConfig;
  secrets: ManagedSecretStatus;
  updatedAt: string;
};

export type LogLevel = "debug" | "info" | "warn" | "error";

export type RuntimeConfig = {
  id: string;
  logLevel: LogLevel;
  guestUploads: GuestUploadConfig;
  registration: RegistrationConfig;
  rateLimits: RuntimeRateLimitConfig;
  limits: {
    freePlanId: string;
    paidPlanIds: string[];
  };
  providerStatus: ProviderStatus;
  alerts: AlertConfig;
  updatedAt: string;
};

export type RuntimeResourceSnapshot = {
  collectedAt: string;
  cpuPercent: number;
  memoryUsedBytes: number;
  memoryTotalBytes: number;
  memoryPercent: number;
  diskUsedBytes: number;
  diskTotalBytes: number;
  diskPercent: number;
  objectStorageBytes: number;
  objectStorageObjectCount: number;
};

export type OperationalMetrics = {
  userCount: number;
  activePastes: number;
  activeStorageBytes: number;
  cleanupQueueDepth: number;
  cleanupFailureDepth: number;
  scanQueueDepth: number;
  scanFailureDepth: number;
  failedJobDepth: number;
  mailQueueDepth: number;
  mailFailedDepth: number;
  reportsOpen: number;
  webhookEvents: number;
  ordersByStatus: Record<string, number>;
};

export type AlertEvent = {
  id: string;
  fingerprint: string;
  level: string;
  message: string;
  status: string;
  lastError?: string;
  sentAt?: string;
  createdAt: string;
  updatedAt: string;
};

export type RuntimePanel = {
  config: RuntimeConfig;
  resources: RuntimeResourceSnapshot;
  operational: OperationalMetrics;
  alerts: AlertEvent[];
};

export type ManualWorkItem = {
  id: string;
  kind: string;
  targetId: string;
  status: string;
  risk?: string;
  summary: string;
  createdAt: string;
  updatedAt: string;
};

export type RedemptionCode = {
  code?: string;
  batchId: string;
  redeemedBy?: string;
  redeemedAt?: string;
  createdAt: string;
};

export type RedemptionBatch = {
  id: string;
  planId: string;
  durationDays: number;
  quantity: number;
  expiresAt?: string;
  maxTotalRedemptions: number;
  maxRedemptionsPerUser: number;
  allowedEmails?: string[];
  allowedDomains?: string[];
  note?: string;
  disabled: boolean;
  redeemedCount: number;
  createdAt: string;
  updatedAt: string;
  codes?: RedemptionCode[];
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

export type SupportContacts = {
  supportEmail: string;
  abuseEmail: string;
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

export type UploadProgress = (loaded: number, total: number) => void;

// uploadWithProgress posts a multipart form through XMLHttpRequest because
// fetch cannot report upload progress, and a multi-file send needs real
// per-file byte progress instead of a timer-driven guess.
export function uploadWithProgress<T>(
  path: string,
  form: FormData,
  onProgress: UploadProgress,
  options: { headers?: Record<string, string>; signal?: AbortSignal } = {},
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    let request: XMLHttpRequest | null = null;
    let settled = false;

    const settle = (finish: () => void) => {
      if (settled) return;
      settled = true;
      options.signal?.removeEventListener("abort", abort);
      finish();
    };
    const abort = () => {
      request?.abort();
    };
    const fail = (error: unknown) => settle(() => reject(error));
    const succeed = (value: T) => settle(() => resolve(value));

    const attempt = (token: string, isRetry: boolean) => {
      const xhr = new XMLHttpRequest();
      request = xhr;
      xhr.open("POST", `/api/v1${path}`);
      xhr.withCredentials = true;
      xhr.setRequestHeader("Accept", "application/json");
      xhr.setRequestHeader("X-CSRF-Token", token);
      for (const [name, value] of Object.entries(options.headers ?? {})) {
        xhr.setRequestHeader(name, value);
      }
      xhr.upload.addEventListener("progress", (event) => {
        if (event.lengthComputable) onProgress(event.loaded, event.total);
      });
      xhr.addEventListener("load", () => {
        const payload = parseJSONBody(xhr.responseText);
        if (xhr.status >= 200 && xhr.status < 300) {
          succeed(payload as T);
          return;
        }
        const error = apiError(xhr.status, xhr.statusText, payload);
        // A cached token can rotate while a queue is uploading, so refresh it
        // once and resend rather than failing every remaining file.
        if (error.code === "csrf_required" && !isRetry) {
          void (async () => {
            try {
              attempt(await fetchCsrfToken(), true);
            } catch (refreshError) {
              fail(refreshError);
            }
          })();
          return;
        }
        fail(error);
      });
      xhr.addEventListener("error", () =>
        fail(new Error("The upload failed before it finished.")),
      );
      xhr.addEventListener("abort", () => fail(abortError()));
      xhr.send(form);
    };

    void (async () => {
      let token = csrfToken;
      if (!token) {
        try {
          token = await fetchCsrfToken();
        } catch (error) {
          fail(error);
          return;
        }
      }
      if (options.signal?.aborted) {
        fail(abortError());
        return;
      }
      options.signal?.addEventListener("abort", abort, { once: true });
      attempt(token, false);
    })();
  });
}

function parseJSONBody(text: string): unknown {
  if (!text) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

// apiError builds the same error shape for both the fetch helper and the
// progress-reporting uploader, so callers read one error contract.
function apiError(
  status: number,
  statusText: string,
  payload: unknown,
): ApiError {
  const body = (payload ?? {}) as { error?: string; message?: string };
  const error = new Error(
    body.message || statusText || "request_failed",
  ) as ApiError;
  error.status = status;
  error.code = body.error ?? "request_failed";
  return error;
}

function abortError(): ApiError {
  const error = new Error("The upload was canceled.") as ApiError;
  error.code = "upload_aborted";
  return error;
}

export function isAbortError(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    (error as { code?: string }).code === "upload_aborted"
  );
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
    throw apiError(
      response.status,
      response.statusText,
      await parseJSONResponse(response),
    );
  }

  if (response.status === 204) {
    return undefined as T;
  }

  return (await response.json()) as T;
}

async function parseJSONResponse(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    return undefined;
  }
}

export const client = {
  plans: () => api<PlanCatalog>("/plans"),
  supportContacts: () => api<SupportContacts>("/support/contacts"),
  me: () => api<User>("/me"),
  register: (body: {
    email: string;
    password: string;
    displayName: string;
    language: string;
    emailVerificationCode?: string;
    turnstileToken?: string;
  }) =>
    api<AuthResult>("/auth/register", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  startRegistrationVerification: (email: string) =>
    api<{ devToken?: string; message: string }>(
      "/auth/registration/email-verification/start",
      {
        method: "POST",
        body: JSON.stringify({ email }),
      },
    ),
  login: (body: { email: string; password: string }) =>
    api<AuthResult>("/auth/login", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  googleOAuthStartPath: (returnTo = "/", language = "") =>
    `/api/v1/auth/google/start?${new URLSearchParams({ returnTo, language }).toString()}`,
  githubOAuthStartPath: (returnTo = "/", language = "") =>
    `/api/v1/auth/github/start?${new URLSearchParams({ returnTo, language }).toString()}`,
  logout: () => api<{ status: string }>("/auth/logout", { method: "POST" }),
  logoutAll: () =>
    api<{ status: string }>("/auth/logout-all", { method: "POST" }),
  startEmailVerification: () =>
    api<{ devToken?: string; message: string }>(
      "/auth/email-verification/start",
      {
        method: "POST",
      },
    ),
  finishEmailVerification: (token: string) =>
    api<User>("/auth/email-verification/finish", {
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
  unlinkOAuth: (provider: string) =>
    api<User>(`/me/oauth/${encodeURIComponent(provider)}`, {
      method: "DELETE",
    }),
  quota: () => api<Quota>("/quota"),
  redeemCode: (code: string) =>
    api<User>("/redemptions/redeem", {
      method: "POST",
      body: JSON.stringify({ code }),
    }),
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
  createGuestPaste: (body: {
    guestToken?: string;
    title: string;
    text: string;
    tags: string[];
    expiresInSeconds: number;
    turnstileToken?: string;
  }) =>
    api<{ guestToken: string; paste: Paste }>("/guest/pastes", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  uploadGuestAttachment: (
    pasteId: string,
    file: File,
    guestToken: string,
    turnstileToken = "",
  ) => {
    const form = new FormData();
    form.append("file", file);
    const headers = new Headers({ "X-PasteBox-Guest-Token": guestToken });
    if (turnstileToken) {
      headers.set("X-PasteBox-Turnstile-Token", turnstileToken);
    }
    return api<Attachment>(`/guest/pastes/${pasteId}/attachments`, {
      method: "POST",
      body: form,
      headers,
    });
  },
  createGuestShare: (
    pasteId: string,
    guestToken: string,
    body: {
      password?: string;
      maxVisits?: number;
      maxDownloads?: number;
      expiresInSeconds: number;
    },
  ) =>
    api<Share>(`/guest/pastes/${pasteId}/shares`, {
      method: "POST",
      body: JSON.stringify({ ...body, guestToken }),
    }),
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
  createTransfer: (body: {
    idempotencyKey?: string;
    expiresInSeconds: number;
    password?: string;
    loginRequired?: boolean;
    // claimQuota is how many anonymous claims the send grants. Zero or absent
    // means the server default of one.
    claimQuota?: number;
    // burnAfterReading asks the server to destroy this send once nobody can be
    // handed it any more. Absent keeps the ordinary expiry-only lifetime.
    burnAfterReading?: boolean;
    // title, text and tags describe the record the send creates: a file send
    // declares items, a text send sends text and no items.
    title?: string;
    text?: string;
    tags?: string[];
    items: TransferItemInput[];
  }) =>
    api<{ transfer: Transfer }>("/transfers", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  // Uploads report real byte progress, so the helper streams through
  // XMLHttpRequest rather than fetch.
  uploadTransferItem: (
    transferId: string,
    itemId: string,
    file: File,
    onProgress: UploadProgress,
    signal?: AbortSignal,
  ) => {
    const form = new FormData();
    form.append("file", file);
    return uploadWithProgress<{ transfer: Transfer; attachment: Attachment }>(
      `/transfers/${encodeURIComponent(transferId)}/items/${encodeURIComponent(itemId)}`,
      form,
      onProgress,
      { signal },
    );
  },
  publishTransfer: (transferId: string) =>
    api<{ transfer: Transfer }>(
      `/transfers/${encodeURIComponent(transferId)}/publish`,
      { method: "POST" },
    ),
  cancelTransfer: (transferId: string) =>
    api<{ transfer: Transfer }>(
      `/transfers/${encodeURIComponent(transferId)}/cancel`,
      { method: "POST" },
    ),
  // resolvePickupCode turns a typed 6-character code into the share token it
  // belongs to, which the recipient then opens like any other share link.
  resolvePickupCode: (code: string) =>
    api<{ token: string; url: string }>("/pickups", {
      method: "POST",
      body: JSON.stringify({ code }),
    }),
  createGuestTransfer: (body: {
    guestToken?: string;
    idempotencyKey?: string;
    expiresInSeconds: number;
    password?: string;
    claimQuota?: number;
    burnAfterReading?: boolean;
    title?: string;
    text?: string;
    items: TransferItemInput[];
    turnstileToken?: string;
  }) =>
    api<{ guestToken: string; transfer: Transfer }>("/guest/transfers", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  uploadGuestTransferItem: (
    transferId: string,
    itemId: string,
    file: File,
    guestToken: string,
    onProgress: UploadProgress,
    options: { turnstileToken?: string; signal?: AbortSignal } = {},
  ) => {
    const form = new FormData();
    form.append("file", file);
    const headers: Record<string, string> = {
      "X-PasteBox-Guest-Token": guestToken,
    };
    if (options.turnstileToken) {
      headers["X-PasteBox-Turnstile-Token"] = options.turnstileToken;
    }
    return uploadWithProgress<{ transfer: Transfer; attachment: Attachment }>(
      `/guest/transfers/${encodeURIComponent(transferId)}/items/${encodeURIComponent(itemId)}`,
      form,
      onProgress,
      { headers, signal: options.signal },
    );
  },
  publishGuestTransfer: (transferId: string, guestToken: string) =>
    api<{ transfer: Transfer }>(
      `/guest/transfers/${encodeURIComponent(transferId)}/publish`,
      { method: "POST", body: JSON.stringify({ guestToken }) },
    ),
  cancelGuestTransfer: (transferId: string, guestToken: string) =>
    api<{ transfer: Transfer }>(
      `/guest/transfers/${encodeURIComponent(transferId)}/cancel`,
      { method: "POST", body: JSON.stringify({ guestToken }) },
    ),
  revokeShare: (id: string) =>
    api<{ status: string }>(`/shares/${id}`, { method: "DELETE" }),
  accessShare: (token: string, password: string) =>
    api<{ paste: Paste; share: Share; transfer?: TransferAccess }>(
      `/shares/${token}/access`,
      {
        method: "POST",
        body: JSON.stringify({ password }),
      },
    ),
  // claimTransfer spends one anonymous claim slot. The operation id makes a
  // retried claim return the same session instead of spending a second slot.
  claimTransfer: (token: string, password: string, operationId: string) =>
    api<TransferClaimResult>(`/shares/${token}/claims`, {
      method: "POST",
      body: JSON.stringify({ password, operationId }),
    }),
  completeTransferClaim: (token: string, claimId: string) =>
    api<{ claim: TransferClaim }>(
      `/shares/${token}/claims/${encodeURIComponent(claimId)}/complete`,
      { method: "POST" },
    ),
  prices: () => api<PlanCatalog>("/billing/prices"),
  orders: () => api<{ orders: Order[] }>("/billing/orders"),
  createOrder: (body: { provider: string; planId: string; period: string }) =>
    api<Order>("/billing/orders", {
      method: "POST",
      body: JSON.stringify(body),
    }),
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
  adminRuntimeConfig: () => api<RuntimeConfig>("/admin/runtime-config"),
  adminManagedConfig: () => api<ManagedConfigView>("/admin/managed-config"),
  adminUpdateManagedConfig: (body: {
    config: Partial<ManagedConfig>;
    fields?: (keyof ManagedConfig)[];
    secrets: ManagedSecretPatch;
  }) =>
    api<ManagedConfigView>("/admin/managed-config", {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  adminUpdateRuntimeConfig: (body: {
    logLevel?: LogLevel;
    guestUploads?: Partial<GuestUploadConfig>;
    registration?: Partial<RegistrationConfig>;
    rateLimits?: Partial<RuntimeRateLimitConfig>;
    alerts?: Partial<AlertConfig>;
  }) =>
    api<RuntimeConfig>("/admin/runtime-config", {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  adminRuntimePanel: () => api<RuntimePanel>("/admin/runtime-panel"),
  adminManualWorkItems: () =>
    api<{ items: ManualWorkItem[] }>("/admin/manual-work-items"),
  adminUpdateCatalog: (body: PlanCatalog) =>
    api<PlanCatalog>("/admin/catalog", {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  adminUpdateCatalogEntries: (
    body: Partial<Pick<PlanCatalog, "plans" | "prices">>,
  ) =>
    api<PlanCatalog>("/admin/catalog/entries", {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  adminProviderTest: (provider: string) =>
    api<RuntimeConfig>(
      `/admin/providers/${encodeURIComponent(provider)}/test`,
      {
        method: "POST",
      },
    ),
  adminRedemptionBatches: () =>
    api<{ batches: RedemptionBatch[] }>("/admin/redemption-batches"),
  adminCreateRedemptionBatch: (body: {
    planId: string;
    durationDays: number;
    quantity: number;
    expiresAt?: string;
    maxTotalRedemptions?: number;
    maxRedemptionsPerUser?: number;
    allowedEmails?: string[];
    allowedDomains?: string[];
    note?: string;
    disabled?: boolean;
  }) =>
    api<RedemptionBatch>("/admin/redemption-batches", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  adminUpdateRedemptionBatch: (
    id: string,
    body: { disabled: boolean; note?: string },
  ) =>
    api<RedemptionBatch>(`/admin/redemption-batches/${id}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  adminAlerts: () => api<{ alerts: AlertEvent[] }>("/admin/alerts"),
  adminSendTestAlert: (message: string) =>
    api<AlertEvent>("/admin/alerts/test", {
      method: "POST",
      body: JSON.stringify({ message }),
    }),
  adminUsers: () => api<{ users: User[] }>("/admin/users"),
  adminSetUserPlan: (
    userId: string,
    body: {
      planId: string;
      expiresAt?: string;
      reason?: string;
      ticketId?: string;
    },
  ) =>
    api<User>(`/admin/users/${encodeURIComponent(userId)}/plan`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
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
  adminMarkOrderPaid: (id: string, txId: string, reason: string) =>
    api<Order>(`/admin/orders/${id}/mark-paid`, {
      method: "POST",
      body: JSON.stringify({ txId, reason }),
    }),
  adminReconcileBilling: () =>
    api<Record<string, number>>("/admin/billing/reconcile", {
      method: "POST",
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

// accountStatusStreamPath is the live channel of the signed-in account: its
// send records, their claim counts and the change markers of its records.
export function accountStatusStreamPath(): string {
  return "/api/v1/me/events";
}

// shareStatusStreamPath is the live channel of one share. The sender's success
// page and the recipient's page use the same channel, because both hold the
// page grant the share hands out.
export function shareStatusStreamPath(token: string): string {
  return `/api/v1/shares/${encodeURIComponent(token)}/events`;
}

export function attachmentDownloadPath(id: string): string {
  return `/api/v1/attachments/${encodeURIComponent(id)}/download`;
}

export function sharedAttachmentDownloadPath(
  token: string,
  attachmentID: string,
): string {
  return `/api/v1/shares/${encodeURIComponent(token)}/attachments/${encodeURIComponent(attachmentID)}/download`;
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
