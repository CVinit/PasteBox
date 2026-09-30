import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import {
  Archive,
  Ban,
  CheckCircle2,
  ClipboardCopy,
  Clock3,
  CreditCard,
  Download,
  FileText,
  FileUp,
  Filter,
  Github,
  Image as ImageIcon,
  KeyRound,
  LifeBuoy,
  Link2,
  LockKeyhole,
  LogOut,
  MailCheck,
  Megaphone,
  Pin,
  RotateCcw,
  Scale,
  Search,
  Send,
  ShieldCheck,
  Snowflake,
  Sparkles,
  Star,
  Tags,
  TimerReset,
  Trash2,
  Undo2,
  UploadCloud,
  UserRound,
} from "lucide-react";

import {
  attachmentDownloadPath,
  client,
  formatBytes,
  formatDuration,
  sharedAttachmentDownloadPath,
  type Attachment,
  type AdminAttachment,
  type AdminQueues,
  type AdminShare,
  type AlertEvent,
  type ApiError,
  type AuditLog,
  type GuestUploadConfig,
  type LogLevel,
  type ManagedConfig,
  type ManagedConfigView,
  type ManagedSecretPatch,
  type ManualWorkItem,
  type Order,
  type Paste,
  type PlanCatalog,
  type Price,
  type Quota,
  type RedemptionBatch,
  type RegistrationConfig,
  type Report,
  type RuntimeConfig,
  type RuntimePanel,
  type Share,
  type SupportContacts,
  type Transfer,
  type TransferItemInput,
  type User,
  type WebhookEvent,
} from "./api";
import {
  useTextSend,
  useTransferQueue,
  type TextSendAdapter,
  type TextSendPhase,
  type TransferQueue,
  type TransferQueueAdapter,
  type TransferQueueItem,
  type TransferQueuePhase,
  type TransferQueueShare,
} from "./transferQueue";
import "./styles.css";

type View = "inbox" | "shared" | "billing" | "settings" | "admin";
type PaymentProvider = "stripe" | "epusdt";

type ViewSummary = {
  eyebrow: string;
  title: string;
  description: string;
};

type WorkspaceStat = {
  label: string;
  value: string;
  detail: string;
  tone: "pastes" | "storage" | "shares" | "attachments";
};

type Draft = {
  title: string;
  text: string;
  tags: string;
  expiresInSeconds: number;
  pinned: boolean;
  favorite: boolean;
};

type ShareDraft = {
  password: string;
  loginRequired: boolean;
  maxVisits: number;
  maxDownloads: number;
  expiresInSeconds: number;
};

// SendSettings are the expiry and privacy choices of one file send. They are
// read when the transfer is created, so they apply to every file in the batch.
type SendSettings = {
  password: string;
  loginRequired: boolean;
};

type RedemptionDraft = {
  planId: string;
  durationDays: number;
  quantity: number;
  note: string;
};

type AdminData = {
  users: User[];
  pastes: Paste[];
  attachments: AdminAttachment[];
  shares: AdminShare[];
  orders: Order[];
  queues: AdminQueues | null;
  webhookEvents: WebhookEvent[];
  runtimeConfig: RuntimeConfig | null;
  managedConfig: ManagedConfigView | null;
  runtimePanel: RuntimePanel | null;
  manualWorkItems: ManualWorkItem[];
  redemptionBatches: RedemptionBatch[];
  alerts: AlertEvent[];
};

type Locale = "en" | "zh-CN" | "zh-TW" | "es";

type OrderStatusTone = "pending" | "success" | "warning" | "danger" | "neutral";
type AttachmentScanTone = "success" | "warning" | "danger" | "neutral";

type OrderStatusDetail = {
  label: string;
  description: string;
  tone: OrderStatusTone;
};

type AttachmentScanDetail = {
  label: string;
  description: string;
  tone: AttachmentScanTone;
  canDownload: boolean;
};

type PublicPage = {
  path: string;
  title: string;
  eyebrow: string;
  summary: string;
  updated: string;
  sections: PublicPageSection[];
};

type PublicPageSection = {
  heading: string;
  body: string[];
  items?: string[];
};

type AuthLinkKind = "email-verification" | "password-reset";

type AuthLink = {
  kind: AuthLinkKind;
  token: string;
};

type AuthMode = "login" | "register";
type AdminTab =
  | "overview"
  | "plans"
  | "guest"
  | "security"
  | "configuration"
  | "services"
  | "queues";
type SizeUnit = "KB" | "MB" | "GB";
type TimeUnit = "seconds" | "minutes" | "hours" | "days";

type AuthFormState = {
  email: string;
  password: string;
  displayName: string;
  emailVerificationCode: string;
  turnstileToken: string;
};

type LandingFeature = {
  title: string;
  body: string;
  stat: string;
};

type LandingContent = {
  navProduct: string;
  navSecurity: string;
  navPricing: string;
  eyebrow: string;
  title: string;
  subtitle: string;
  primaryCta: string;
  secondaryCta: string;
  workspaceLabel: string;
  workspaceTitle: string;
  workspaceBody: string;
  features: LandingFeature[];
  steps: Array<{ title: string; body: string }>;
  ctaTitle: string;
  ctaBody: string;
  ctaBadges: string[];
};

const defaultDraft: Draft = {
  title: "",
  text: "",
  tags: "",
  expiresInSeconds: 24 * 60 * 60,
  pinned: false,
  favorite: false,
};

const defaultShareDraft: ShareDraft = {
  password: "",
  loginRequired: false,
  maxVisits: 5,
  maxDownloads: 5,
  expiresInSeconds: 24 * 60 * 60,
};

// The existing policy default: a send keeps the record's expiry and starts
// without a password, so nothing is protected until the sender asks for it.
const defaultSendSettings: SendSettings = {
  password: "",
  loginRequired: false,
};

const defaultRedemptionDraft: RedemptionDraft = {
  planId: "plus",
  durationDays: 30,
  quantity: 1,
  note: "",
};

const emptyAdminData: AdminData = {
  users: [],
  pastes: [],
  attachments: [],
  shares: [],
  orders: [],
  queues: null,
  webhookEvents: [],
  runtimeConfig: null,
  managedConfig: null,
  runtimePanel: null,
  manualWorkItems: [],
  redemptionBatches: [],
  alerts: [],
};

const supportedLocales: Array<{ value: Locale; label: string }> = [
  { value: "en", label: "English" },
  { value: "zh-CN", label: "简体中文" },
  { value: "zh-TW", label: "繁體中文" },
  { value: "es", label: "Español" },
];

const adminTabOptions: Array<{ value: AdminTab; labelKey: string }> = [
  { value: "overview", labelKey: "adminTabOverview" },
  { value: "plans", labelKey: "adminTabPlans" },
  { value: "guest", labelKey: "adminTabGuest" },
  { value: "security", labelKey: "adminTabSecurity" },
  { value: "configuration", labelKey: "adminTabConfiguration" },
  { value: "services", labelKey: "adminTabServices" },
  { value: "queues", labelKey: "adminTabQueues" },
];
const logLevelOptions: LogLevel[] = ["debug", "info", "warn", "error"];

const clayHeroAsset = "/assets/clay-hero-ai.png";
const clayFooterAsset = "/assets/clay-footer-ai.png";

const viewSummaries: Record<Locale, Record<View, ViewSummary>> = {
  en: {
    inbox: {
      eyebrow: "Unified workspace",
      title: "Save content in one place. Pick it up anytime.",
      description:
        "Text, images, and files land in one focused workspace, so you can find them, add attachments, and share faster.",
    },
    shared: {
      eyebrow: "Share management",
      title: "Every link is trackable and revocable.",
      description:
        "See visits, downloads, and expiry at a glance, then pull a link back when access should stop.",
    },
    billing: {
      eyebrow: "Plan benefits",
      title: "Choose the plan that fits how you share.",
      description:
        "Free covers quick transfers; Plus adds more room and longer retention; Pro is built for large files, frequent sharing, and ongoing projects.",
    },
    settings: {
      eyebrow: "Account and security",
      title: "Keep account, data, and safety requests together.",
      description:
        "Manage sign-in options, export data, report abuse, and request deletion without jumping between pages.",
    },
    admin: {
      eyebrow: "Operations console",
      title: "See key signals together and act faster.",
      description:
        "Users, content, orders, queues, and alerts stay in one view so operators can spot risk and finish launch checks.",
    },
  },
  "zh-CN": {
    inbox: {
      eyebrow: "统一工作台",
      title: "集中保存内容，随时继续处理。",
      description:
        "文字、图片和文件会进入同一个清爽工作区，方便查找、补充附件并快速生成分享。",
    },
    shared: {
      eyebrow: "分享管理",
      title: "每一条链接都可追踪、可撤销。",
      description:
        "访问次数、下载状态和过期时间集中展示，发现风险时可以立即收回分享权限。",
    },
    billing: {
      eyebrow: "套餐权益",
      title: "按使用场景选择更合适的套餐。",
      description:
        "Free 适合临时传输；Plus 提供更高容量和更长有效期；Pro 面向大文件、高频分享和长期项目。",
    },
    settings: {
      eyebrow: "账号与安全",
      title: "把账号、数据和安全请求放在一起。",
      description:
        "绑定登录方式、导出数据、提交举报和删除申请都在这里完成，减少来回切换。",
    },
    admin: {
      eyebrow: "运营控制台",
      title: "关键状态集中看，问题处理更快。",
      description:
        "用户、内容、订单、队列和告警集中在同一处，方便管理员判断风险和推进上线检查。",
    },
  },
  "zh-TW": {
    inbox: {
      eyebrow: "統一工作台",
      title: "集中儲存內容，隨時繼續處理。",
      description:
        "文字、圖片和檔案會進入同一個清爽工作區，方便查找、補充附件並快速產生分享。",
    },
    shared: {
      eyebrow: "分享管理",
      title: "每一條連結都可追蹤、可撤銷。",
      description:
        "訪問次數、下載狀態和過期時間集中展示，發現風險時可以立即收回分享權限。",
    },
    billing: {
      eyebrow: "方案權益",
      title: "依照使用場景選擇更合適的方案。",
      description:
        "Free 適合臨時傳輸；Plus 提供更高容量和更長有效期；Pro 面向大檔案、高頻分享和長期專案。",
    },
    settings: {
      eyebrow: "帳號與安全",
      title: "把帳號、資料和安全請求放在一起。",
      description:
        "綁定登入方式、匯出資料、提交檢舉和刪除申請都在這裡完成，減少來回切換。",
    },
    admin: {
      eyebrow: "營運控制台",
      title: "關鍵狀態集中看，問題處理更快。",
      description:
        "使用者、內容、訂單、佇列和警示集中在同一處，方便管理員判斷風險和推進上線檢查。",
    },
  },
  es: {
    inbox: {
      eyebrow: "Espacio de trabajo unificado",
      title: "Guarda contenido en un lugar y retómalo cuando quieras.",
      description:
        "Texto, imágenes y archivos quedan en un espacio claro para encontrarlos, añadir adjuntos y compartirlos más rápido.",
    },
    shared: {
      eyebrow: "Gestión de enlaces",
      title: "Cada enlace se puede revisar y revocar.",
      description:
        "Consulta visitas, descargas y caducidad de un vistazo, y retira el acceso cuando deje de ser seguro.",
    },
    billing: {
      eyebrow: "Beneficios del plan",
      title: "Elige el plan que encaja con tu forma de compartir.",
      description:
        "Free cubre envíos puntuales; Plus añade más capacidad y más retención; Pro está pensado para archivos grandes, uso frecuente y proyectos continuos.",
    },
    settings: {
      eyebrow: "Cuenta y seguridad",
      title: "Cuenta, datos y seguridad en un solo lugar.",
      description:
        "Gestiona métodos de acceso, exportaciones, reportes de abuso y solicitudes de eliminación sin cambiar de página.",
    },
    admin: {
      eyebrow: "Consola de operaciones",
      title: "Señales clave juntas para actuar antes.",
      description:
        "Usuarios, contenido, pedidos, colas y alertas se ven en una misma vista para detectar riesgos y cerrar revisiones de lanzamiento.",
    },
  },
};

const shareTokenFromPath =
  typeof window !== "undefined" && window.location.pathname.startsWith("/s/")
    ? decodeURIComponent(window.location.pathname.slice(3))
    : "";

function authLinkFromLocation(): AuthLink | null {
  if (typeof window === "undefined") return null;
  const token = new URLSearchParams(window.location.search)
    .get("token")
    ?.trim();
  if (!token) return null;
  const path = window.location.pathname.replace(/\/+$/, "") || "/";
  switch (path) {
    case "/email-verification":
      return { kind: "email-verification", token };
    case "/password-reset":
      return { kind: "password-reset", token };
    default:
      return null;
  }
}

function clearAuthLinkTokenFromLocation() {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  url.searchParams.delete("token");
  const search = url.searchParams.toString();
  window.history.replaceState(
    null,
    "",
    `${url.pathname}${search ? `?${search}` : ""}${url.hash}`,
  );
}

function localeFor(language?: string): Locale {
  const normalized = language?.trim().toLowerCase() ?? "";
  if (
    normalized === "zh-tw" ||
    normalized === "zh-hk" ||
    normalized === "zh-mo" ||
    normalized.includes("hant")
  ) {
    return "zh-TW";
  }
  if (
    normalized === "zh-cn" ||
    normalized === "zh-sg" ||
    normalized.startsWith("zh")
  ) {
    return "zh-CN";
  }
  if (normalized.startsWith("es")) return "es";
  return "en";
}

function localeFromRequestParams(): Locale | null {
  if (typeof window === "undefined") return null;
  const params = new URLSearchParams(window.location.search);
  const requested =
    params.get("lang") ?? params.get("locale") ?? params.get("hl") ?? "";
  if (!requested.trim()) return null;
  return localeFor(requested);
}

function isChineseLocale(locale: Locale): boolean {
  return locale.startsWith("zh");
}

function browserPreferredLocale(): Locale {
  if (typeof navigator === "undefined") return "zh-CN";
  const languages = navigator.languages?.length
    ? navigator.languages
    : [navigator.language];
  return localeFor(languages.find(Boolean));
}

const browserLocale: Locale = browserPreferredLocale();

const publicPages: PublicPage[] = [
  {
    path: "/legal",
    eyebrow: "Legal center",
    title: "PasteBox Legal And Support Hub",
    summary:
      "Public entry point for PasteBox terms, privacy, refunds, abuse handling, account rights, subprocessors, and status updates.",
    updated: "2026-05-26",
    sections: [
      {
        heading: "How to use this hub",
        body: [
          "Use these pages to understand the rules for public beta use, how PasteBox handles uploaded content, and how to contact support for account, billing, privacy, or abuse requests.",
        ],
        items: [
          "Terms and Privacy explain the baseline service contract and data handling.",
          "Refund, Abuse/DMCA, Account Deletion, and Data Export explain user request paths.",
          "Data Retention and Subprocessors describe the production architecture commitments.",
        ],
      },
    ],
  },
  {
    path: "/legal/terms",
    eyebrow: "Terms",
    title: "Terms Of Service",
    summary:
      "The baseline service rules for using PasteBox during public beta with free and paid plans.",
    updated: "2026-05-26",
    sections: [
      {
        heading: "Service scope",
        body: [
          "PasteBox provides private paste, temporary file transfer, sharing, billing, export, and account-management features. You are responsible for the content you upload and share.",
          "Paid plan access depends on successful provider confirmation through Stripe or Epusdt. PasteBox may suspend or revoke access for abuse, payment failure, security risk, or policy violations.",
        ],
      },
      {
        heading: "User responsibilities",
        body: [
          "Do not upload or share illegal, abusive, malicious, infringing, or harmful content. Do not attempt to bypass scan gates, quota limits, authentication, billing, rate limits, or administrative controls.",
        ],
        items: [
          "Keep account credentials secure.",
          "Use share links only for content you have the right to distribute.",
          "Report abuse or payment issues through the support and abuse paths listed in this hub.",
        ],
      },
      {
        heading: "Operational changes",
        body: [
          "During beta, PasteBox may change plan limits, provider integrations, retention rules, or safety gates when needed for security, reliability, compliance, or product operation.",
        ],
      },
    ],
  },
  {
    path: "/legal/privacy",
    eyebrow: "Privacy",
    title: "Privacy Policy",
    summary:
      "How PasteBox handles account data, uploaded content metadata, billing records, support requests, and operational logs.",
    updated: "2026-05-26",
    sections: [
      {
        heading: "Data collected",
        body: [
          "PasteBox stores account profile data, authentication state, paste metadata, attachment metadata, private object keys, share settings, quota usage, orders, webhook events, audit logs, reports, support records, and export/deletion request state.",
          "Attachment bytes are stored in private S3-compatible object storage. PostgreSQL stores metadata and lifecycle state. Redis-compatible services are not the source of truth.",
        ],
      },
      {
        heading: "How data is used",
        body: [
          "Data is used to operate the service, enforce quotas and scan policy, process billing, deliver account and security emails, respond to support and abuse requests, generate exports, delete accounts, and investigate security or abuse events.",
        ],
      },
      {
        heading: "Request paths",
        body: [
          "Use the in-app export and deletion controls in Settings for normal account requests. Use the Support page for GDPR/data-subject requests, DPA requests, billing disputes, and privacy escalations.",
        ],
      },
    ],
  },
  {
    path: "/legal/refund",
    eyebrow: "Billing",
    title: "Refund Policy",
    summary:
      "How refund and payment-support requests are handled for Stripe and Epusdt payments.",
    updated: "2026-05-26",
    sections: [
      {
        heading: "Eligible requests",
        body: [
          "Refund requests are reviewed for duplicate charges, provider processing errors, accidental purchases, service unavailability, and plan-access disputes. Refund approval depends on provider evidence, account history, and whether paid benefits were consumed.",
        ],
      },
      {
        heading: "Provider handling",
        body: [
          "Stripe refunds and cancellations are reconciled through signed provider webhooks. Epusdt fixed-duration orders are reviewed against transaction evidence, order expiry, and any manual correction audit trail.",
        ],
      },
      {
        heading: "How to request help",
        body: [
          "Open the Support page and include your account email, order ID, payment provider, payment time, and a concise description. Do not send card numbers, private keys, seed phrases, or raw secrets.",
        ],
      },
    ],
  },
  {
    path: "/legal/abuse",
    eyebrow: "Abuse and DMCA",
    title: "Abuse And DMCA Policy",
    summary:
      "How PasteBox receives and triages abuse, malware, copyright, and takedown reports.",
    updated: "2026-05-26",
    sections: [
      {
        heading: "Reportable content",
        body: [
          "Reports may cover malware, phishing, illegal content, harassment, copyright infringement, exposed secrets, spam, or share links that violate PasteBox terms.",
        ],
      },
      {
        heading: "Triage actions",
        body: [
          "PasteBox may revoke share links, freeze attachments, block malicious files, preserve audit evidence, request more information, or suspend accounts. Known malicious files are blocked globally for owner downloads, public access, previews, exports, and future shares.",
        ],
      },
      {
        heading: "Required report details",
        body: [
          "Use the in-app report form or Support page. Include the PasteBox URL or target ID, reason, evidence, contact email, and whether the request is urgent. DMCA notices should identify the copyrighted work and the allegedly infringing content.",
        ],
      },
    ],
  },
  {
    path: "/legal/cookies",
    eyebrow: "Cookies",
    title: "Cookie Notice",
    summary:
      "PasteBox uses essential cookies for authentication, CSRF protection, and OAuth state.",
    updated: "2026-05-26",
    sections: [
      {
        heading: "Current cookie use",
        body: [
          "PasteBox currently uses essential cookies only: the session cookie, CSRF double-submit cookie, and Google OAuth state cookie. These cookies are required for secure browser operation.",
        ],
      },
      {
        heading: "Non-essential cookies",
        body: [
          "PasteBox does not currently use non-essential analytics or advertising cookies. If non-essential cookies are added later, the product must add a consent control before enabling them.",
        ],
      },
    ],
  },
  {
    path: "/status",
    eyebrow: "Status",
    title: "Status And Announcements",
    summary:
      "Public status baseline for public beta launch readiness and incident communication.",
    updated: "2026-05-26",
    sections: [
      {
        heading: "Current beta gate",
        body: [
          "PasteBox is still gated by the production launch roadmap. Public beta requires durable persistence, object storage, auth/email/OAuth, scan policy, billing readiness, operations evidence, and legal/support surfaces to pass their launch gates.",
        ],
      },
      {
        heading: "Incident updates",
        body: [
          "During production operation, this page is the public entry point for availability notices, maintenance windows, degraded provider status, and post-incident summaries.",
        ],
      },
    ],
  },
  {
    path: "/support",
    eyebrow: "Support",
    title: "Support Contact",
    summary:
      "How users request help for accounts, billing, abuse, data rights, and operational issues.",
    updated: "2026-05-26",
    sections: [
      {
        heading: "Support intake",
        body: [
          "Use the in-app report form for abuse tied to a paste, share, or attachment. For account, billing, privacy, DPA, or data-subject requests, contact support with your account email and the relevant target ID.",
        ],
        items: [
          "Billing and refunds: include order ID, provider, amount, and timestamp.",
          "Abuse/DMCA: include target URL or ID, evidence, and requested action.",
          "GDPR/data-subject or DPA: include account email, request type, and verification contact.",
        ],
      },
      {
        heading: "Response records",
        body: [
          "Support/admin actions must be tracked through reports, audit logs, order records, and account lifecycle records so requests can be reviewed without direct database access.",
        ],
      },
    ],
  },
  {
    path: "/legal/account-deletion",
    eyebrow: "Account rights",
    title: "Account Deletion Instructions",
    summary:
      "How users request, cancel, and execute account deletion in PasteBox.",
    updated: "2026-05-26",
    sections: [
      {
        heading: "In-app deletion path",
        body: [
          "Sign in, open Settings, select Delete request, and review the scheduled deletion state. You can cancel the request before execution or execute deletion when the account is eligible.",
        ],
      },
      {
        heading: "Support deletion path",
        body: [
          "If you cannot access the account, contact Support with the account email and verification details. Support will verify ownership before acting on deletion requests.",
        ],
      },
    ],
  },
  {
    path: "/legal/data-export",
    eyebrow: "Account rights",
    title: "Data Export Instructions",
    summary:
      "How users export account, paste, share, order, and audit-visible data.",
    updated: "2026-05-26",
    sections: [
      {
        heading: "In-app export path",
        body: [
          "Sign in, open Settings, and select Export. The browser downloads a JSON export for the current account through the authenticated `/api/v1/me/export` endpoint.",
        ],
      },
      {
        heading: "Support export path",
        body: [
          "For data-subject requests that require additional review, use Support and include the account email, jurisdiction or request type, and verification contact.",
        ],
      },
    ],
  },
  {
    path: "/legal/data-retention",
    eyebrow: "Retention",
    title: "Data Retention Matrix",
    summary:
      "Retention commitments aligned with the production architecture and public beta launch target.",
    updated: "2026-05-26",
    sections: [
      {
        heading: "Runtime data",
        body: [
          "Pastes, attachments, shares, orders, reports, jobs, mails, and audit logs are retained according to account state, paste expiry, cleanup jobs, billing obligations, abuse evidence needs, and backup retention.",
        ],
        items: [
          "Backups: 30-day retention with daily logical backups plus WAL/PITR evidence.",
          "Expired/deleted content: removed by cleanup jobs and object lifecycle cleanup.",
          "Audit and billing records: retained as needed for security, disputes, abuse, and compliance.",
        ],
      },
    ],
  },
  {
    path: "/legal/subprocessors",
    eyebrow: "Subprocessors",
    title: "Subprocessors",
    summary:
      "Provider categories used by the confirmed single-VPS public beta architecture.",
    updated: "2026-05-26",
    sections: [
      {
        heading: "Provider categories",
        body: [
          "The launch architecture uses a US single-region VPS/cloud host, managed S3-compatible object storage, existing SMTP/enterprise email, Stripe, Epusdt, Google OAuth, and off-host S3-compatible backup storage.",
        ],
      },
      {
        heading: "Change handling",
        body: [
          "Subprocessor changes must update this page, the data-retention matrix when relevant, and the production runbooks before the new provider is used for public beta data.",
        ],
      },
    ],
  },
];

const publicLinks = [
  { href: "/legal/terms", labelKey: "terms" },
  { href: "/legal/privacy", labelKey: "privacy" },
  { href: "/legal/refund", labelKey: "refund" },
  { href: "/legal/abuse", labelKey: "abuseDmca" },
  { href: "/legal/cookies", labelKey: "cookies" },
  { href: "/status", labelKey: "status" },
  { href: "/support", labelKey: "support" },
  { href: "/legal/account-deletion", labelKey: "deletion" },
  { href: "/legal/data-export", labelKey: "export" },
  { href: "/legal/data-retention", labelKey: "retention" },
  { href: "/legal/subprocessors", labelKey: "subprocessors" },
];

function publicPageForPath(pathname: string): PublicPage | null {
  const normalized = pathname.replace(/\/+$/, "") || "/";
  return publicPages.find((page) => page.path === normalized) ?? null;
}

function normalizedPathname(): string {
  if (typeof window === "undefined") return "/";
  return window.location.pathname.replace(/\/+$/, "") || "/";
}

function authModeForPath(pathname: string): AuthMode | null {
  const normalized = pathname.replace(/\/+$/, "") || "/";
  if (normalized === "/register") return "register";
  if (
    normalized === "/login" ||
    normalized === "/password-reset" ||
    normalized === "/email-verification"
  ) {
    return "login";
  }
  return null;
}

function isWorkspacePath(pathname: string): boolean {
  const normalized = pathname.replace(/\/+$/, "") || "/";
  return normalized === "/app";
}

function moveToWorkspacePath() {
  if (typeof window === "undefined") return;
  if (isWorkspacePath(window.location.pathname)) return;
  window.history.replaceState(null, "", "/app");
}

const baseCopy: Record<"en" | "zh-CN", Record<string, string>> = {
  en: {
    privateCloudClipboard: "File transfer · cross-device",
    sharedPaste: "Shared paste",
    showImage: "Show image",
    hideImage: "Hide image",
    email: "Email",
    password: "Password",
    displayName: "Display name",
    login: "Login",
    register: "Register",
    google: "Google",
    github: "GitHub",
    emailName: "Email name",
    emailDomain: "Email domain",
    registrationCode: "Registration code",
    sendRegistrationCode: "Send code",
    registrationCodeIssued: "Registration code sent",
    turnstileChallenge: "Security check",
    turnstileNotConfigured: "Turnstile is not configured",
    verificationToken: "verification token",
    verify: "Verify",
    useToken: "Use token",
    manualTokenFallback: "Manual token fallback",
    reset: "Reset",
    resetToken: "reset token",
    forgotPassword: "Forgot password?",
    sendResetEmail: "Send reset email",
    newPassword: "New password",
    backToLogin: "Back to login",
    updatePassword: "Update password",
    inbox: "Inbox",
    shares: "Shares",
    billing: "Billing",
    settings: "Settings",
    admin: "Admin",
    currentPlan: "Current plan",
    pastes: "pastes",
    logout: "Logout",
    logoutAll: "Logout all",
    search: "Search",
    all: "All",
    text: "Text",
    images: "Images",
    files: "Files",
    expiring: "Expiring",
    shared: "Shared",
    favorites: "Favorites",
    emailVerificationRequired: "Email verification required",
    send: "Send",
    newPrivatePaste: "New private paste",
    private: "Private",
    title: "Title",
    tags: "tags",
    tagsPerPaste: "Tags per paste",
    tagLimit: "Tag limit",
    upgradeForTags: "Upgrade to add tags",
    tagReadOnly: "Tags are read-only on this plan",
    filteredByTag: "Filtered by tag",
    clearTagFilter: "Clear tag filter",
    create: "Create",
    upload: "Upload",
    recentPastes: "Recent pastes",
    untitledPaste: "Untitled paste",
    createSecureDrop: "Create a secure drop.",
    titleThisPaste: "Title this paste",
    pasteTextPlaceholder:
      "Paste text, notes, credentials, or transfer context here.",
    tagsSeparatedByComma: "tags separated by comma",
    dropOrChooseFile: "Drop or choose a file",
    duration1Hour: "1 hour",
    duration6Hours: "6 hours",
    duration24Hours: "24 hours",
    duration7Days: "7 days",
    duration30Days: "30 days",
    duration180Days: "180 days",
    active: "active",
    edit: "Edit",
    noPasteSelected: "No paste selected",
    save: "Save",
    share: "Share",
    createShare: "Create",
    open: "Open",
    report: "Report",
    revoke: "Revoke",
    loginRequired: "login required",
    anonymous: "anonymous",
    expires: "expires",
    visits: "visits",
    downloads: "downloads",
    shareToken: "share token",
    maxVisits: "max visits",
    maxDownloads: "max downloads",
    attachmentsCount: "attachments",
    stripeUsdtPayments: "Plans for every transfer size",
    billingStatusTitle: "Payment status and membership",
    billingSupport: "Billing support",
    billingSupportBody:
      "Refunds, duplicate charges, stuck Epusdt payments, and manual review requests are handled through the refund policy and support intake.",
    refundPolicy: "Refund Policy",
    support: "Support",
    legalHub: "Legal hub",
    terms: "Terms",
    refund: "Refund",
    contentRules: "Do not upload illegal, malicious, or infringing content.",
    factCapacity: "Capacity",
    factRetention: "Retention",
    factFileLimit: "Per file",
    factSignup: "Sign-in",
    factSignupNotRequired: "Not required",
    factSignupRequired: "Required",
    abuseDmca: "Abuse/DMCA",
    cookies: "Cookies",
    status: "Status",
    deletion: "Deletion",
    openApp: "Open app",
    launchDocuments: "Launch documents",
    legalNavigation: "Legal navigation",
    footerLegal: "Legal",
    footerTrust: "Trust",
    footerSupport: "Support",
    lastUpdated: "Last updated",
    supportRequests:
      "Account, billing, privacy, DPA, and data-subject requests",
    abuseRequests: "Abuse, malware, DMCA, and urgent takedown requests",
    supportContactLoading: "Support contact is loading",
    abuseContactLoading: "Abuse contact is loading",
    publicDocFooter:
      "This page reflects the confirmed public-beta launch architecture and must be updated when providers, retention, or request workflows change.",
    storage: "Storage",
    file: "File",
    retention: "Retention",
    subprocessors: "Subprocessors",
    traffic: "Traffic",
    activePastes: "active pastes",
    activePastesLabel: "Active pastes",
    planLimit: "plan limit",
    storageOf: "of",
    sharedLinks: "Shared links",
    pastesExposed: "pastes exposed",
    attachmentsLabel: "Attachments",
    expiringIn24h: "expiring in 24h",
    noUrgentExpiry: "No urgent expiry",
    unavailable: "Unavailable",
    openCheckout: "Open checkout",
    paymentAddress: "address",
    webhook: "Webhook",
    accountActive: "Account active",
    deletionScheduled: "Deletion scheduled",
    saveProfile: "Save profile",
    linkedAccounts: "Linked accounts",
    noLinkedAccounts: "No external login providers linked.",
    unlinkGoogle: "Unlink Google",
    oauthUnlinked: "OAuth account unlinked",
    export: "Export",
    deleteRequest: "Delete request",
    cancelDelete: "Cancel delete",
    deleteNow: "Delete now",
    accountRights: "Account rights",
    accountRightsBody:
      "Review data export, account deletion, privacy, and support intake before submitting sensitive requests.",
    dataExport: "data export",
    accountDeletion: "account deletion",
    privacy: "privacy",
    supportIntake: "support intake",
    reportTarget: "report target",
    reportReason: "report reason",
    auditQueuesCleanup: "Audit, queues, cleanup",
    adminTabOverview: "Overview",
    adminTabPlans: "Plans",
    adminTabGuest: "Guest limits",
    adminTabSecurity: "Security",
    adminTabServices: "Services",
    adminTabConfiguration: "Application config",
    adminTabQueues: "Queues & audit",
    adminControlPanel: "Control panel",
    activeStorageUsage: "Active storage",
    openReports: "Open reports",
    scanQueue: "Scan queue",
    clientId: "Client ID",
    clientSecret: "Client secret",
    redirectUrl: "Redirect URL",
    webhookSecret: "Webhook secret",
    checkoutUrl: "Checkout URL",
    merchantId: "Merchant ID",
    provider: "Provider",
    logMailer: "Log only",
    scannerHeuristic: "Heuristic scan",
    configurationReady: "Configured",
    configurationIncomplete: "Incomplete",
    missingConfiguration: "Missing",
    providerTestOk: "Last test passed",
    providerTestMissingConfig: "Test blocked by missing configuration",
    providerTestSendFailed: "Test delivery failed",
    alertCpuHigh: "PasteBox CPU usage high",
    alertMemoryHigh: "PasteBox memory usage high",
    alertDiskHigh: "PasteBox disk usage high",
    alertObjectStorageHigh: "PasteBox object storage usage high",
    alertScanFailuresHigh: "PasteBox scan failure queue depth",
    alertFailedJobsHigh: "PasteBox failed job depth",
    alertFailedMailsHigh: "PasteBox failed mail depth",
    alertOpenReportsHigh: "PasteBox open report count",
    maliciousAttachment: "Malicious attachment",
    statusQueued: "Queued",
    statusPending: "Pending",
    statusFailed: "Failed",
    statusOpen: "Open",
    statusSent: "Sent",
    statusSuppressed: "Suppressed",
    statusResolved: "Resolved",
    statusDismissed: "Dismissed",
    runtimeConfig: "Runtime config",
    processLogs: "Process logs",
    logLevel: "Log level",
    logLevelDebug: "Debug",
    logLevelInfo: "Info",
    logLevelWarn: "Warn",
    logLevelError: "Error",
    resourcePanel: "Resources",
    loadingSession: "Checking session…",
    openWorkspace: "Open workspace",
    saving: "Saving…",
    configSection: "Settings group",
    planLimits: "Plan limits",
    priceSettings: "Prices",
    choosePlan: "Choose plan",
    choosePrice: "Choose price",
    savePlan: "Save this plan",
    savePrice: "Save this price",
    periodMonthly: "Monthly",
    periodYearly: "Yearly",

    planCatalog: "Plans and prices",
    providerStatus: "Provider status",
    manualReview: "Manual review",
    redemptionCodes: "Redemption codes",
    alertHistory: "Alert history",
    saveCatalog: "Save catalog",
    catalogSaved: "Catalog saved",
    runtimeConfigSaved: "Runtime config saved",
    saveRuntimeConfig: "Save runtime config",
    applicationConfig: "Application configuration",
    applicationName: "Application name",
    siteSettings: "Site settings",
    publicUrl: "Public URL",
    supportEmail: "Support email",
    abuseEmail: "Abuse email",
    corsOrigins: "Allowed CORS origins",
    workerHeartbeatMaxAge: "Worker heartbeat max age",
    endpoint: "Endpoint",
    bucket: "Bucket",
    region: "Region",
    pathStyle: "Use path-style requests",
    accessKey: "Access key",
    secretKey: "Secret key",
    mailService: "Mail service",
    smtpHost: "SMTP host",
    smtpPort: "SMTP port",
    username: "Username",
    fromEmail: "From email",
    fromName: "From name",
    tlsMode: "TLS mode",
    oauthAndCaptcha: "OAuth and anti-abuse",
    turnstileSecretKey: "Turnstile secret key",
    verifyUrl: "Verification URL",
    scannerAndNotifications: "Scanner and notifications",
    scannerProvider: "Scanner provider",
    clamavAddress: "ClamAV address",
    scannerTimeout: "Scanner timeout",
    telegramChatId: "Telegram chat ID",
    telegramBotToken: "Telegram bot token",
    apiBaseUrl: "API base URL",
    paymentProviders: "Payment providers",
    chain: "Chain",
    port: "port",
    secretConfigured: "configured",
    secretMissing: "not configured",
    secretKeepPlaceholder: "Leave blank to keep the current value",
    clearSecret: "Clear saved secret",
    saveApplicationConfig: "Save application config",
    managedConfigSaved: "Application config saved",
    guestUploads: "Guest uploads",
    registrationSecurity: "Registration security",
    allowedEmailDomains: "Allowed email domains",
    allowedEmailDomainsPlaceholder: "gmail.com, outlook.com",
    requireEmailVerification: "Require email code",
    turnstileSiteKey: "Turnstile site key",
    rateLimits: "Rate limits",
    rateLimitWindow: "Rate limit window",
    emailVerificationLimit: "Email code sends",
    registerLimit: "Registrations",
    loginLimit: "Logins",
    writeLimit: "Writes",
    uploadLimit: "Uploads",
    shareCreateLimit: "Share creates",
    shareAccessLimit: "Share opens",
    downloadLimit: "Downloads",
    webhookLimit: "Webhooks",
    requireTurnstile: "Require Turnstile",
    enabled: "Enabled",
    disabled: "Disabled",
    telegramAlerts: "Telegram alerts",
    telegramDelivery: "Telegram delivery",
    silentAlert: "Silent alert",
    cooldownSeconds: "Cooldown seconds",
    cpuThreshold: "CPU threshold %",
    memoryThreshold: "Memory threshold %",
    diskThreshold: "Disk threshold %",
    objectStorageThreshold: "Object storage threshold",
    scanFailureThreshold: "Scan failure threshold",
    failedJobThreshold: "Failed job threshold",
    failedMailThreshold: "Failed mail threshold",
    openReportThreshold: "Open report threshold",
    sendTestAlert: "Send test alert",
    alertSent: "Alert sent",
    providerTested: "Provider tested",
    createRedemptionBatch: "Create batch",
    redemptionBatchCreated: "Redemption batch created",
    redemptionBatchUpdated: "Redemption batch updated",
    durationDays: "Duration days",
    quantity: "Quantity",
    note: "Note",
    objectStorage: "Object storage",
    cpu: "CPU",
    memory: "Memory",
    disk: "Disk",
    activePasteLimitShort: "Active limit",
    activeStorageLimit: "Active storage",
    singleTextLimit: "Single text",
    singleFileLimit: "Single file",
    singlePasteLimit: "Single paste",
    attachmentsPerPaste: "Attachments per paste",
    retentionSeconds: "Retention seconds",
    dailyUploadLimit: "Daily upload",
    dailyShareDownloadLimit: "Daily share download",
    period: "Period",
    currency: "Currency",
    priceCents: "Price cents",
    visible: "Visible",
    purchase: "Purchase",
    noRecentAlerts: "No recent alerts",
    noManualWorkItems: "No manual work items",
    runCleanup: "Run cleanup",
    runBillingReconcile: "Run billing reconcile",
    users: "Users",
    attachments: "Attachments",
    orders: "Orders",
    paid: "Paid",
    manualPaymentReason: "Manual payment reason",
    manualPaymentReasonPlaceholder: "Support ticket or correction reason",
    manualPaymentReasonRequired: "Enter a support reason before marking paid",
    queues: "Queues",
    scanFailures: "Scan failures",
    cleanupJobs: "Cleanup jobs",
    cleanupFailures: "Cleanup failures",
    failedJobs: "Failed jobs",
    queuedMails: "Queued mails",
    failedMails: "Failed mails",
    attempts: "attempts",
    deleteFailures: "Delete failures",
    resolve: "Resolve",
    dismiss: "Dismiss",
    retry: "Retry",
    release: "Release",
    freeze: "Freeze",
    frozen: "frozen",
    webhooks: "Webhooks",
    replay: "Replay",
    copy: "Copy",
    accountReady: "Account ready",
    signedIn: "Signed in",
    signedInWithGoogle: "Signed in with Google",
    verificationIssued: "Verification issued",
    emailVerified: "Email verified",
    emailVerifiedLogin: "Email verified. Sign in to continue.",
    emailVerifiedDifferentAccount:
      "Email verified for another account. Sign out before switching.",
    passwordResetLinkReady:
      "Enter a new password to finish resetting your account.",
    signedOut: "Signed out",
    allSessionsSignedOut: "All sessions signed out",
    logoutAllDevices: "Sign out all device sessions",
    logoutAllDevicesDescription:
      "End every active PasteBox session on this browser and other devices. You will need to sign in again everywhere.",
    passwordResetIssued: "Reset email sent. Open it to continue.",
    passwordUpdated: "Password updated",
    reportSubmitted: "Report submitted",
    pasteCreated: "Paste created",
    attachmentUploaded: "Attachment uploaded",
    shareLinkCreated: "Share link created",
    shareOpened: "Share opened",
    pasteUpdated: "Paste updated",
    pinUpdated: "Pin updated",
    favoriteUpdated: "Favorite updated",
    expirationExtended: "Expiration extended",
    orderCreated: "Order created",
    exportGenerated: "Export generated",
    deletionCanceled: "Deletion canceled",
    accountDeleted: "Account deleted",
    profileUpdated: "Profile updated",
    cleanupCompleted: "Cleanup completed",
    billingReconciled: "Billing reconciliation completed",
    scanRetried: "Scan retried",
    attachmentFrozen: "Attachment frozen",
    attachmentReleased: "Attachment released",
    shareRevoked: "Share revoked",
    orderMarkedPaid: "Order marked paid",
    webhookProcessed: "Webhook processed",
    webhookReplayed: "Webhook replayed",
    reportUpdated: "Report updated",
    pasteDeleted: "Paste deleted",
    requestFailed: "Request failed",
    unpinPaste: "Unpin paste",
    pinPaste: "Pin paste",
    removeFavorite: "Remove favorite",
    favoritePaste: "Favorite paste",
    copyText: "Copy text",
    extendPaste: "Extend paste",
    deletePaste: "Delete paste",
    unit: "unit",
    unitKB: "KB",
    unitMB: "MB",
    unitGB: "GB",
    unitseconds: "Seconds",
    unitminutes: "Minutes",
    unithours: "Hours",
    unitdays: "Days",
    unitItems: "items",
    unitFiles: "files",
    unitPercent: "%",
    unitRequests: "requests",
  },
  "zh-CN": {
    privateCloudClipboard: "文件中转 · 跨设备传文件",
    sharedPaste: "分享内容",
    showImage: "预览图片",
    hideImage: "收起图片",
    email: "邮箱",
    password: "密码",
    displayName: "显示名",
    login: "登录",
    register: "注册",
    google: "Google",
    github: "GitHub",
    emailName: "邮箱名",
    emailDomain: "邮箱后缀",
    registrationCode: "注册验证码",
    sendRegistrationCode: "发送验证码",
    registrationCodeIssued: "注册验证码已发送",
    turnstileChallenge: "安全验证",
    turnstileNotConfigured: "Turnstile 尚未配置",
    verificationToken: "邮箱验证码",
    verify: "验证",
    useToken: "使用令牌",
    manualTokenFallback: "手动令牌备用入口",
    reset: "重置",
    resetToken: "重置令牌",
    forgotPassword: "忘记密码？",
    sendResetEmail: "发送重置邮件",
    newPassword: "新密码",
    backToLogin: "返回登录",
    updatePassword: "更新密码",
    inbox: "收件箱",
    shares: "分享",
    billing: "会员",
    settings: "设置",
    admin: "后台",
    currentPlan: "当前套餐",
    pastes: "条内容",
    logout: "退出",
    logoutAll: "退出全部",
    search: "搜索",
    all: "全部",
    text: "文本",
    images: "图片",
    files: "文件",
    expiring: "即将过期",
    shared: "已分享",
    favorites: "收藏",
    emailVerificationRequired: "需要邮箱验证",
    send: "发送",
    newPrivatePaste: "新建私有内容",
    private: "私有",
    title: "标题",
    tags: "标签",
    tagsPerPaste: "每条标签数",
    tagLimit: "标签上限",
    upgradeForTags: "升级后可添加标签",
    tagReadOnly: "当前套餐下标签只读",
    filteredByTag: "按标签筛选",
    clearTagFilter: "清除标签筛选",
    create: "创建",
    upload: "上传",
    recentPastes: "最近内容",
    untitledPaste: "未命名内容",
    createSecureDrop: "创建安全投放。",
    titleThisPaste: "为这个内容命名",
    pasteTextPlaceholder: "在此粘贴文本、备注、凭据或传输上下文。",
    tagsSeparatedByComma: "用英文逗号分隔标签",
    dropOrChooseFile: "拖放或选择文件",
    duration1Hour: "1 小时",
    duration6Hours: "6 小时",
    duration24Hours: "24 小时",
    duration7Days: "7 天",
    duration30Days: "30 天",
    duration180Days: "180 天",
    active: "有效",
    edit: "编辑",
    noPasteSelected: "未选择内容",
    save: "保存",
    share: "分享",
    createShare: "创建",
    open: "打开",
    report: "举报",
    revoke: "撤销",
    loginRequired: "需要登录",
    anonymous: "匿名访问",
    expires: "过期",
    visits: "次访问",
    downloads: "次下载",
    shareToken: "分享令牌",
    maxVisits: "最大访问次数",
    maxDownloads: "最大下载次数",
    attachmentsCount: "个附件",
    stripeUsdtPayments: "按传输规模选择套餐",
    billingStatusTitle: "支付状态和会员权益",
    billingSupport: "支付支持",
    billingSupportBody:
      "退款、重复扣款、卡住的 Epusdt 支付和人工审核请求会通过退款政策和支持入口处理。",
    refundPolicy: "退款政策",
    support: "支持",
    legalHub: "法律中心",
    terms: "条款",
    refund: "退款",
    contentRules: "禁止上传违法、恶意或侵权内容。",
    factCapacity: "单次容量",
    factRetention: "保留期",
    factFileLimit: "单文件上限",
    factSignup: "注册要求",
    factSignupNotRequired: "无需注册",
    factSignupRequired: "需注册",
    abuseDmca: "滥用/DMCA",
    cookies: "Cookie",
    status: "状态",
    deletion: "删除",
    openApp: "打开应用",
    launchDocuments: "上线文档",
    legalNavigation: "法律导航",
    footerLegal: "法律",
    footerTrust: "信任",
    footerSupport: "支持",
    lastUpdated: "最后更新",
    supportRequests: "账号、支付、隐私、DPA 和数据主体请求",
    abuseRequests: "滥用、恶意软件、DMCA 和紧急下架请求",
    supportContactLoading: "支持联系人加载中",
    abuseContactLoading: "滥用联系人加载中",
    publicDocFooter:
      "本页面反映已确认的公开 beta 上线架构；当提供商、保留策略或请求流程变化时必须更新。",
    storage: "存储",
    file: "文件",
    retention: "有效期",
    subprocessors: "子处理方",
    traffic: "流量",
    activePastes: "条有效内容",
    activePastesLabel: "有效内容",
    planLimit: "套餐上限",
    storageOf: "共",
    sharedLinks: "分享链接",
    pastesExposed: "条内容已公开",
    attachmentsLabel: "附件",
    expiringIn24h: "24 小时内过期",
    noUrgentExpiry: "暂无紧急过期",
    unavailable: "不可购买",
    openCheckout: "打开支付页",
    paymentAddress: "收款地址",
    webhook: "Webhook",
    accountActive: "账号正常",
    deletionScheduled: "已计划删除",
    saveProfile: "保存资料",
    linkedAccounts: "关联账号",
    noLinkedAccounts: "尚未关联外部登录方式。",
    unlinkGoogle: "解除 Google 关联",
    oauthUnlinked: "OAuth 账号已解除关联",
    export: "导出",
    deleteRequest: "申请删除",
    cancelDelete: "取消删除",
    deleteNow: "立即删除",
    accountRights: "账号权利",
    accountRightsBody:
      "提交敏感请求前，请先查看数据导出、账号删除、隐私和支持入口。",
    dataExport: "数据导出",
    accountDeletion: "账号删除",
    privacy: "隐私",
    supportIntake: "支持入口",
    reportTarget: "举报目标",
    reportReason: "举报原因",
    auditQueuesCleanup: "审计、队列、清理",
    adminTabOverview: "概览",
    adminTabPlans: "套餐/价格",
    adminTabGuest: "游客限制",
    adminTabSecurity: "安全/限流",
    adminTabServices: "服务配置",
    adminTabConfiguration: "应用配置",
    adminTabQueues: "队列/审计",
    adminControlPanel: "控制面板",
    activeStorageUsage: "已用存储",
    openReports: "待处理举报",
    scanQueue: "扫描队列",
    clientId: "客户端 ID",
    clientSecret: "客户端密钥",
    redirectUrl: "回调地址",
    webhookSecret: "Webhook 密钥",
    checkoutUrl: "支付地址",
    merchantId: "商户 ID",
    provider: "提供方",
    logMailer: "仅记录日志",
    scannerHeuristic: "启发式扫描",
    configurationReady: "配置完整",
    configurationIncomplete: "配置不完整",
    missingConfiguration: "缺少",
    providerTestOk: "最近测试通过",
    providerTestMissingConfig: "缺少配置，未执行测试",
    providerTestSendFailed: "测试发送失败",
    alertCpuHigh: "PasteBox CPU 使用率过高",
    alertMemoryHigh: "PasteBox 内存使用率过高",
    alertDiskHigh: "PasteBox 磁盘使用率过高",
    alertObjectStorageHigh: "PasteBox 对象存储用量过高",
    alertScanFailuresHigh: "PasteBox 扫描失败队列数量",
    alertFailedJobsHigh: "PasteBox 失败任务数量",
    alertFailedMailsHigh: "PasteBox 失败邮件数量",
    alertOpenReportsHigh: "PasteBox 待处理举报数量",
    maliciousAttachment: "恶意附件",
    statusQueued: "排队中",
    statusPending: "等待中",
    statusFailed: "失败",
    statusOpen: "待处理",
    statusSent: "已发送",
    statusSuppressed: "已抑制",
    statusResolved: "已解决",
    statusDismissed: "已忽略",
    runtimeConfig: "运行配置",
    processLogs: "进程日志",
    logLevel: "日志等级",
    logLevelDebug: "调试",
    logLevelInfo: "信息",
    logLevelWarn: "警告",
    logLevelError: "错误",
    resourcePanel: "资源面板",
    loadingSession: "正在读取登录状态…",
    openWorkspace: "进入工作区",
    saving: "正在保存…",
    configSection: "设置分组",
    planLimits: "套餐额度",
    priceSettings: "价格设置",
    choosePlan: "选择套餐",
    choosePrice: "选择价格",
    savePlan: "保存此套餐",
    savePrice: "保存此价格",
    periodMonthly: "月付",
    periodYearly: "年付",

    planCatalog: "套餐和价格",
    providerStatus: "服务配置状态",
    manualReview: "人工处理",
    redemptionCodes: "兑换码",
    alertHistory: "告警记录",
    saveCatalog: "保存套餐",
    catalogSaved: "套餐已保存",
    runtimeConfigSaved: "运行配置已保存",
    saveRuntimeConfig: "保存运行配置",
    applicationConfig: "应用配置",
    applicationName: "应用名称",
    siteSettings: "站点设置",
    publicUrl: "公开地址",
    supportEmail: "客服邮箱",
    abuseEmail: "投诉邮箱",
    corsOrigins: "允许的 CORS 来源",
    workerHeartbeatMaxAge: "工作进程心跳最大间隔",
    endpoint: "服务地址",
    bucket: "存储桶",
    region: "区域",
    pathStyle: "使用路径风格请求",
    accessKey: "访问密钥",
    secretKey: "私密密钥",
    mailService: "邮件服务",
    smtpHost: "SMTP 主机",
    smtpPort: "SMTP 端口",
    username: "用户名",
    fromEmail: "发件邮箱",
    fromName: "发件名称",
    tlsMode: "TLS 模式",
    oauthAndCaptcha: "第三方登录与人机验证",
    turnstileSecretKey: "Turnstile 私钥",
    verifyUrl: "验证地址",
    scannerAndNotifications: "扫描与通知",
    scannerProvider: "扫描服务",
    clamavAddress: "ClamAV 地址",
    scannerTimeout: "扫描超时",
    telegramChatId: "Telegram Chat ID",
    telegramBotToken: "Telegram Bot Token",
    apiBaseUrl: "API 基础地址",
    paymentProviders: "支付渠道",
    chain: "链",
    port: "端口",
    secretConfigured: "已配置",
    secretMissing: "未配置",
    secretKeepPlaceholder: "留空则保留当前值",
    clearSecret: "清除已保存密钥",
    saveApplicationConfig: "保存应用配置",
    managedConfigSaved: "应用配置已保存",
    guestUploads: "游客上传",
    registrationSecurity: "注册安全",
    allowedEmailDomains: "允许注册的邮箱后缀",
    allowedEmailDomainsPlaceholder: "gmail.com, outlook.com",
    requireEmailVerification: "要求邮箱验证码",
    turnstileSiteKey: "Turnstile 站点密钥",
    rateLimits: "接口限流",
    rateLimitWindow: "限流窗口",
    emailVerificationLimit: "验证码发送次数",
    registerLimit: "注册次数",
    loginLimit: "登录次数",
    writeLimit: "写入次数",
    uploadLimit: "上传次数",
    shareCreateLimit: "创建分享次数",
    shareAccessLimit: "打开分享次数",
    downloadLimit: "下载次数",
    webhookLimit: "Webhook 次数",
    requireTurnstile: "要求 Turnstile",
    enabled: "已开启",
    disabled: "已关闭",
    telegramAlerts: "Telegram 告警",
    telegramDelivery: "Telegram 发送",
    silentAlert: "静默告警",
    cooldownSeconds: "冷却秒数",
    cpuThreshold: "CPU 阈值 %",
    memoryThreshold: "内存阈值 %",
    diskThreshold: "磁盘阈值 %",
    objectStorageThreshold: "对象存储阈值",
    scanFailureThreshold: "扫描失败阈值",
    failedJobThreshold: "失败任务阈值",
    failedMailThreshold: "失败邮件阈值",
    openReportThreshold: "未处理举报阈值",
    sendTestAlert: "发送测试告警",
    alertSent: "告警已发送",
    providerTested: "服务测试已完成",
    createRedemptionBatch: "创建批次",
    redemptionBatchCreated: "兑换码批次已创建",
    redemptionBatchUpdated: "兑换码批次已更新",
    durationDays: "有效天数",
    quantity: "数量",
    note: "备注",
    objectStorage: "对象存储",
    cpu: "CPU",
    memory: "内存",
    disk: "磁盘",
    activePasteLimitShort: "有效内容上限",
    activeStorageLimit: "有效存储上限",
    singleTextLimit: "单文本上限",
    singleFileLimit: "单文件上限",
    singlePasteLimit: "单条内容上限",
    attachmentsPerPaste: "每条附件数",
    retentionSeconds: "保留秒数",
    dailyUploadLimit: "每日上传上限",
    dailyShareDownloadLimit: "每日分享下载上限",
    period: "周期",
    currency: "币种",
    priceCents: "价格分",
    visible: "可见",
    purchase: "可购买",
    noRecentAlerts: "暂无近期告警",
    noManualWorkItems: "暂无人工处理项",
    runCleanup: "运行清理",
    runBillingReconcile: "运行支付对账",
    users: "用户",
    attachments: "附件",
    orders: "订单",
    paid: "标记支付",
    manualPaymentReason: "人工支付原因",
    manualPaymentReasonPlaceholder: "客服工单或修正原因",
    manualPaymentReasonRequired: "标记支付前请输入客服原因",
    queues: "队列",
    scanFailures: "扫描失败",
    cleanupJobs: "清理任务",
    cleanupFailures: "清理失败",
    failedJobs: "失败任务",
    queuedMails: "排队邮件",
    failedMails: "失败邮件",
    attempts: "次尝试",
    deleteFailures: "删除失败",
    resolve: "处理",
    dismiss: "驳回",
    retry: "重试",
    release: "解冻",
    freeze: "冻结",
    frozen: "已冻结",
    webhooks: "Webhook",
    replay: "重放",
    copy: "复制",
    accountReady: "账号已创建",
    signedIn: "已登录",
    signedInWithGoogle: "已通过 Google 登录",
    verificationIssued: "验证令牌已发送",
    emailVerified: "邮箱已验证",
    emailVerifiedLogin: "邮箱已验证，请登录后继续。",
    emailVerifiedDifferentAccount:
      "另一个账号的邮箱已验证，切换前请先退出当前账号。",
    passwordResetLinkReady: "请输入新密码以完成账号密码重置。",
    signedOut: "已退出",
    allSessionsSignedOut: "所有会话已退出",
    logoutAllDevices: "退出所有设备会话",
    logoutAllDevicesDescription:
      "结束当前浏览器和其他设备上的全部 PasteBox 登录会话，之后所有设备都需要重新登录。",
    passwordResetIssued: "重置邮件已发送，请打开邮件继续。",
    passwordUpdated: "密码已更新",
    reportSubmitted: "举报已提交",
    pasteCreated: "内容已创建",
    attachmentUploaded: "附件已上传",
    shareLinkCreated: "分享链接已创建",
    shareOpened: "分享已打开",
    pasteUpdated: "内容已更新",
    pinUpdated: "置顶已更新",
    favoriteUpdated: "收藏已更新",
    expirationExtended: "有效期已延长",
    orderCreated: "订单已创建",
    exportGenerated: "导出已生成",
    deletionCanceled: "删除已取消",
    accountDeleted: "账号已删除",
    profileUpdated: "资料已更新",
    cleanupCompleted: "清理完成",
    billingReconciled: "支付对账已完成",
    scanRetried: "扫描已重试",
    attachmentFrozen: "附件已冻结",
    attachmentReleased: "附件已解冻",
    shareRevoked: "分享已撤销",
    orderMarkedPaid: "订单已标记支付",
    webhookProcessed: "Webhook 已处理",
    webhookReplayed: "Webhook 已重放",
    reportUpdated: "举报已更新",
    pasteDeleted: "内容已删除",
    requestFailed: "请求失败",
    unpinPaste: "取消置顶内容",
    pinPaste: "置顶内容",
    removeFavorite: "移除收藏",
    favoritePaste: "收藏内容",
    copyText: "复制文本",
    extendPaste: "延长内容",
    deletePaste: "删除内容",
    unit: "单位",
    unitKB: "KB",
    unitMB: "MB",
    unitGB: "GB",
    unitseconds: "秒",
    unitminutes: "分钟",
    unithours: "小时",
    unitdays: "天",
    unitItems: "条",
    unitFiles: "个文件",
    unitPercent: "%",
    unitRequests: "次",
  },
};

const copy: Record<Locale, Record<string, string>> = {
  en: baseCopy.en,
  "zh-CN": baseCopy["zh-CN"],
  "zh-TW": {
    ...baseCopy["zh-CN"],
    privateCloudClipboard: "檔案中轉 · 跨裝置傳檔案",
    sharedPaste: "分享內容",
    showImage: "預覽圖片",
    hideImage: "收起圖片",
    email: "電子郵件",
    password: "密碼",
    displayName: "顯示名稱",
    login: "登入",
    register: "註冊",
    verificationToken: "電子郵件驗證碼",
    verify: "驗證",
    useToken: "使用權杖",
    manualTokenFallback: "手動權杖備用入口",
    reset: "重設",
    resetToken: "重設權杖",
    forgotPassword: "忘記密碼？",
    sendResetEmail: "寄送重設郵件",
    newPassword: "新密碼",
    backToLogin: "返回登入",
    updatePassword: "更新密碼",
    inbox: "收件匣",
    shares: "分享",
    billing: "方案",
    settings: "設定",
    admin: "後台",
    currentPlan: "目前方案",
    pastes: "則內容",
    logout: "登出",
    logoutAll: "登出全部",
    search: "搜尋",
    all: "全部",
    text: "文字",
    images: "圖片",
    files: "檔案",
    expiring: "即將過期",
    shared: "已分享",
    favorites: "收藏",
    emailVerificationRequired: "需要電子郵件驗證",
    send: "傳送",
    newPrivatePaste: "新增私有內容",
    private: "私有",
    title: "標題",
    tags: "標籤",
    tagsPerPaste: "每則標籤數",
    tagLimit: "標籤上限",
    upgradeForTags: "升級後可新增標籤",
    tagReadOnly: "目前方案下標籤唯讀",
    filteredByTag: "依標籤篩選",
    clearTagFilter: "清除標籤篩選",
    create: "建立",
    upload: "上傳",
    recentPastes: "最近內容",
    untitledPaste: "未命名內容",
    createSecureDrop: "建立安全投放。",
    titleThisPaste: "為這個內容命名",
    pasteTextPlaceholder: "在此貼上文字、備註、憑證或傳輸上下文。",
    tagsSeparatedByComma: "用英文逗號分隔標籤",
    dropOrChooseFile: "拖放或選擇檔案",
    duration1Hour: "1 小時",
    duration6Hours: "6 小時",
    duration24Hours: "24 小時",
    duration7Days: "7 天",
    duration30Days: "30 天",
    duration180Days: "180 天",
    active: "有效",
    edit: "編輯",
    noPasteSelected: "未選擇內容",
    save: "儲存",
    share: "分享",
    open: "開啟",
    report: "檢舉",
    revoke: "撤銷",
    loginRequired: "需要登入",
    anonymous: "匿名訪問",
    expires: "過期",
    visits: "次訪問",
    downloads: "次下載",
    shareToken: "分享權杖",
    maxVisits: "最大訪問次數",
    maxDownloads: "最大下載次數",
    attachmentsCount: "個附件",
    billingSupport: "付款支援",
    support: "支援",
    legalHub: "法律中心",
    terms: "條款",
    refund: "退款",
    contentRules: "禁止上傳違法、惡意或侵權內容。",
    factCapacity: "單次容量",
    factRetention: "保留期",
    factFileLimit: "單一檔案上限",
    factSignup: "註冊要求",
    factSignupNotRequired: "無需註冊",
    factSignupRequired: "需註冊",
    abuseDmca: "濫用/DMCA",
    cookies: "Cookie",
    status: "狀態",
    deletion: "刪除",
    openApp: "開啟應用",
    launchDocuments: "上線文件",
    legalNavigation: "法律導覽",
    footerLegal: "法律",
    footerTrust: "信任",
    footerSupport: "支援",
    lastUpdated: "最後更新",
    supportRequests: "帳號、付款、隱私、DPA 和資料主體請求",
    abuseRequests: "濫用、惡意軟體、DMCA 和緊急下架請求",
    supportContactLoading: "支援聯絡人載入中",
    abuseContactLoading: "濫用聯絡人載入中",
    publicDocFooter:
      "本頁面反映已確認的公開 beta 上線架構；當提供商、保留策略或請求流程變化時必須更新。",
    storage: "儲存",
    file: "檔案",
    retention: "保留期限",
    subprocessors: "子處理方",
    traffic: "流量",
    activePastes: "則有效內容",
    activePastesLabel: "有效內容",
    planLimit: "方案上限",
    storageOf: "共",
    sharedLinks: "分享連結",
    pastesExposed: "則內容已公開",
    attachmentsLabel: "附件",
    expiringIn24h: "24 小時內過期",
    noUrgentExpiry: "暫無緊急過期",
    unavailable: "不可購買",
    openCheckout: "開啟付款頁",
    paymentAddress: "收款地址",
    accountActive: "帳號正常",
    deletionScheduled: "已排程刪除",
    saveProfile: "儲存資料",
    linkedAccounts: "已連結帳號",
    noLinkedAccounts: "尚未連結外部登入方式。",
    unlinkGoogle: "解除 Google 連結",
    oauthUnlinked: "OAuth 帳號已解除連結",
    export: "匯出",
    deleteRequest: "申請刪除",
    cancelDelete: "取消刪除",
    deleteNow: "立即刪除",
    accountRights: "帳號權利",
    accountRightsBody:
      "提交敏感請求前，請先查看資料匯出、帳號刪除、隱私和支援入口。",
    dataExport: "資料匯出",
    accountDeletion: "帳號刪除",
    privacy: "隱私",
    supportIntake: "支援入口",
    reportTarget: "檢舉目標",
    reportReason: "檢舉原因",
    auditQueuesCleanup: "稽核、佇列、清理",
    adminTabConfiguration: "應用設定",
    adminControlPanel: "控制面板",
    activeStorageUsage: "已用儲存",
    openReports: "待處理檢舉",
    scanQueue: "掃描佇列",
    clientId: "客戶端 ID",
    clientSecret: "客戶端密鑰",
    redirectUrl: "回呼地址",
    webhookSecret: "Webhook 密鑰",
    checkoutUrl: "付款地址",
    merchantId: "商戶 ID",
    provider: "提供者",
    logMailer: "僅記錄日誌",
    scannerHeuristic: "啟發式掃描",
    configurationReady: "設定完整",
    configurationIncomplete: "設定不完整",
    missingConfiguration: "缺少",
    providerTestOk: "最近測試通過",
    providerTestMissingConfig: "缺少設定，未執行測試",
    providerTestSendFailed: "測試傳送失敗",
    alertCpuHigh: "PasteBox CPU 使用率過高",
    alertMemoryHigh: "PasteBox 記憶體使用率過高",
    alertDiskHigh: "PasteBox 磁碟使用率過高",
    alertObjectStorageHigh: "PasteBox 物件儲存用量過高",
    alertScanFailuresHigh: "PasteBox 掃描失敗佇列數量",
    alertFailedJobsHigh: "PasteBox 失敗任務數量",
    alertFailedMailsHigh: "PasteBox 失敗郵件數量",
    alertOpenReportsHigh: "PasteBox 待處理檢舉數量",
    maliciousAttachment: "惡意附件",
    statusQueued: "排隊中",
    statusPending: "等待中",
    statusFailed: "失敗",
    statusOpen: "待處理",
    statusSent: "已傳送",
    statusSuppressed: "已抑制",
    statusResolved: "已解決",
    statusDismissed: "已忽略",
    runtimeConfig: "執行設定",
    processLogs: "程序日誌",
    logLevel: "日誌等級",
    logLevelDebug: "偵錯",
    logLevelInfo: "資訊",
    logLevelWarn: "警告",
    logLevelError: "錯誤",
    resourcePanel: "資源面板",
    loadingSession: "正在讀取登入狀態…",
    openWorkspace: "進入工作區",
    saving: "正在儲存…",
    configSection: "設定分組",
    planLimits: "方案額度",
    priceSettings: "價格設定",
    choosePlan: "選擇方案",
    choosePrice: "選擇價格",
    savePlan: "儲存此方案",
    savePrice: "儲存此價格",
    periodMonthly: "月付",
    periodYearly: "年付",

    planCatalog: "方案和價格",
    providerStatus: "服務設定狀態",
    manualReview: "人工處理",
    redemptionCodes: "兌換碼",
    alertHistory: "告警記錄",
    saveCatalog: "儲存方案",
    catalogSaved: "方案已儲存",
    runtimeConfigSaved: "執行設定已儲存",
    saveRuntimeConfig: "儲存執行設定",
    applicationConfig: "應用設定",
    applicationName: "應用名稱",
    siteSettings: "站點設定",
    publicUrl: "公開網址",
    supportEmail: "客服信箱",
    abuseEmail: "投訴信箱",
    corsOrigins: "允許的 CORS 來源",
    workerHeartbeatMaxAge: "工作程序心跳最大間隔",
    endpoint: "服務位址",
    bucket: "儲存桶",
    region: "區域",
    pathStyle: "使用路徑樣式請求",
    accessKey: "存取密鑰",
    secretKey: "私密密鑰",
    mailService: "郵件服務",
    smtpHost: "SMTP 主機",
    smtpPort: "SMTP 連接埠",
    username: "使用者名稱",
    fromEmail: "寄件信箱",
    fromName: "寄件名稱",
    tlsMode: "TLS 模式",
    oauthAndCaptcha: "第三方登入與人機驗證",
    turnstileSecretKey: "Turnstile 私鑰",
    verifyUrl: "驗證網址",
    scannerAndNotifications: "掃描與通知",
    scannerProvider: "掃描服務",
    clamavAddress: "ClamAV 位址",
    scannerTimeout: "掃描逾時",
    telegramChatId: "Telegram Chat ID",
    telegramBotToken: "Telegram Bot Token",
    apiBaseUrl: "API 基礎網址",
    paymentProviders: "付款管道",
    chain: "鏈",
    port: "連接埠",
    secretConfigured: "已設定",
    secretMissing: "未設定",
    secretKeepPlaceholder: "留空則保留目前值",
    clearSecret: "清除已儲存密鑰",
    saveApplicationConfig: "儲存應用設定",
    managedConfigSaved: "應用設定已儲存",
    guestUploads: "訪客上傳",
    requireTurnstile: "要求 Turnstile",
    enabled: "已開啟",
    disabled: "已關閉",
    telegramAlerts: "Telegram 告警",
    telegramDelivery: "Telegram 傳送",
    silentAlert: "靜默告警",
    cooldownSeconds: "冷卻秒數",
    cpuThreshold: "CPU 閾值 %",
    memoryThreshold: "記憶體閾值 %",
    diskThreshold: "磁碟閾值 %",
    objectStorageThreshold: "物件儲存閾值",
    scanFailureThreshold: "掃描失敗閾值",
    failedJobThreshold: "失敗任務閾值",
    failedMailThreshold: "失敗郵件閾值",
    openReportThreshold: "未處理檢舉閾值",
    sendTestAlert: "傳送測試告警",
    alertSent: "告警已傳送",
    providerTested: "服務測試已完成",
    createRedemptionBatch: "建立批次",
    redemptionBatchCreated: "兌換碼批次已建立",
    redemptionBatchUpdated: "兌換碼批次已更新",
    durationDays: "有效天數",
    quantity: "數量",
    note: "備註",
    objectStorage: "物件儲存",
    cpu: "CPU",
    memory: "記憶體",
    disk: "磁碟",
    activePasteLimitShort: "有效內容上限",
    activeStorageLimit: "有效儲存上限",
    singleTextLimit: "單文字上限",
    singleFileLimit: "單檔上限",
    singlePasteLimit: "單則內容上限",
    attachmentsPerPaste: "每則附件數",
    retentionSeconds: "保留秒數",
    dailyUploadLimit: "每日上傳上限",
    dailyShareDownloadLimit: "每日分享下載上限",
    period: "週期",
    currency: "幣別",
    priceCents: "價格分",
    visible: "可見",
    purchase: "可購買",
    noRecentAlerts: "暫無近期告警",
    noManualWorkItems: "暫無人工處理項",
    runCleanup: "執行清理",
    runBillingReconcile: "執行付款對帳",
    users: "使用者",
    attachments: "附件",
    orders: "訂單",
    paid: "標記已付款",
    manualPaymentReason: "人工付款原因",
    manualPaymentReasonPlaceholder: "客服工單或修正原因",
    manualPaymentReasonRequired: "標記付款前請輸入客服原因",
    queues: "佇列",
    scanFailures: "掃描失敗",
    cleanupJobs: "清理任務",
    cleanupFailures: "清理失敗",
    failedJobs: "失敗任務",
    queuedMails: "排隊郵件",
    failedMails: "失敗郵件",
    attempts: "次嘗試",
    deleteFailures: "刪除失敗",
    resolve: "處理",
    dismiss: "駁回",
    retry: "重試",
    release: "解凍",
    freeze: "凍結",
    frozen: "已凍結",
    replay: "重放",
    copy: "複製",
    accountReady: "帳號已建立",
    signedIn: "已登入",
    signedInWithGoogle: "已透過 Google 登入",
    verificationIssued: "驗證權杖已傳送",
    emailVerified: "電子郵件已驗證",
    emailVerifiedLogin: "電子郵件已驗證，請登入後繼續。",
    emailVerifiedDifferentAccount:
      "另一個帳號的電子郵件已驗證，切換前請先登出目前帳號。",
    passwordResetLinkReady: "請輸入新密碼以完成帳號密碼重設。",
    signedOut: "已登出",
    allSessionsSignedOut: "所有工作階段已登出",
    logoutAllDevices: "登出所有裝置工作階段",
    logoutAllDevicesDescription:
      "結束目前瀏覽器和其他裝置上的所有 PasteBox 登入工作階段，之後所有裝置都需要重新登入。",
    passwordResetIssued: "重設郵件已寄出，請開啟郵件繼續。",
    passwordUpdated: "密碼已更新",
    reportSubmitted: "檢舉已提交",
    pasteCreated: "內容已建立",
    attachmentUploaded: "附件已上傳",
    shareLinkCreated: "分享連結已建立",
    shareOpened: "分享已開啟",
    pasteUpdated: "內容已更新",
    pinUpdated: "置頂已更新",
    favoriteUpdated: "收藏已更新",
    expirationExtended: "有效期已延長",
    orderCreated: "訂單已建立",
    exportGenerated: "匯出已產生",
    deletionCanceled: "刪除已取消",
    accountDeleted: "帳號已刪除",
    profileUpdated: "資料已更新",
    cleanupCompleted: "清理完成",
    billingReconciled: "付款對帳已完成",
    scanRetried: "掃描已重試",
    attachmentFrozen: "附件已凍結",
    attachmentReleased: "附件已解除凍結",
    shareRevoked: "分享已撤銷",
    orderMarkedPaid: "訂單已標記付款",
    webhookProcessed: "Webhook 已處理",
    webhookReplayed: "Webhook 已重放",
    reportUpdated: "檢舉已更新",
    pasteDeleted: "內容已刪除",
    requestFailed: "請求失敗",
    unpinPaste: "取消置頂內容",
    pinPaste: "置頂內容",
    removeFavorite: "移除收藏",
    favoritePaste: "收藏內容",
    copyText: "複製文字",
    extendPaste: "延長內容",
    deletePaste: "刪除內容",
  },
  es: {
    ...baseCopy.en,
    privateCloudClipboard: "Transferencia de archivos",
    sharedPaste: "Paste compartido",
    showImage: "Ver imagen",
    hideImage: "Ocultar imagen",
    email: "Correo",
    password: "Contraseña",
    displayName: "Nombre visible",
    login: "Iniciar sesión",
    register: "Registrarse",
    google: "Google",
    verificationToken: "token de verificación",
    verify: "Verificar",
    useToken: "Usar token",
    manualTokenFallback: "Entrada manual de token",
    reset: "Restablecer",
    resetToken: "token de restablecimiento",
    forgotPassword: "¿Olvidaste tu contraseña?",
    sendResetEmail: "Enviar correo de restablecimiento",
    newPassword: "Contraseña nueva",
    backToLogin: "Volver al inicio",
    updatePassword: "Actualizar contraseña",
    inbox: "Bandeja",
    shares: "Enlaces",
    billing: "Facturación",
    settings: "Ajustes",
    admin: "Admin",
    currentPlan: "Plan actual",
    pastes: "pastes",
    logout: "Salir",
    logoutAll: "Salir de todo",
    search: "Buscar",
    all: "Todo",
    text: "Texto",
    images: "Imágenes",
    files: "Archivos",
    expiring: "Por vencer",
    shared: "Compartido",
    favorites: "Favoritos",
    emailVerificationRequired: "Verificación de correo requerida",
    send: "Enviar",
    newPrivatePaste: "Nuevo paste privado",
    private: "Privado",
    title: "Título",
    tags: "etiquetas",
    tagsPerPaste: "Etiquetas por paste",
    tagLimit: "Límite de etiquetas",
    upgradeForTags: "Mejora para añadir etiquetas",
    tagReadOnly: "Etiquetas de solo lectura en este plan",
    filteredByTag: "Filtrado por etiqueta",
    clearTagFilter: "Quitar filtro de etiqueta",
    create: "Crear",
    upload: "Subir",
    recentPastes: "Pastes recientes",
    untitledPaste: "Paste sin título",
    createSecureDrop: "Crea una entrega segura.",
    titleThisPaste: "Titula este paste",
    pasteTextPlaceholder:
      "Pega texto, notas, credenciales o contexto de transferencia aquí.",
    tagsSeparatedByComma: "etiquetas separadas por coma",
    dropOrChooseFile: "Arrastra o elige un archivo",
    duration1Hour: "1 hora",
    duration6Hours: "6 horas",
    duration24Hours: "24 horas",
    duration7Days: "7 días",
    duration30Days: "30 días",
    duration180Days: "180 días",
    active: "activos",
    edit: "Editar",
    noPasteSelected: "Ningún paste seleccionado",
    save: "Guardar",
    share: "Compartir",
    createShare: "Crear",
    open: "Abrir",
    report: "Reportar",
    revoke: "Revocar",
    loginRequired: "requiere inicio de sesión",
    anonymous: "anónimo",
    expires: "vence",
    visits: "visitas",
    downloads: "descargas",
    shareToken: "token de enlace",
    maxVisits: "visitas máximas",
    maxDownloads: "descargas máximas",
    attachmentsCount: "adjuntos",
    stripeUsdtPayments: "Ciclo de pago de Stripe y USDT",
    billingSupport: "Soporte de facturación",
    billingSupportBody:
      "Reembolsos, cargos duplicados, pagos Epusdt atascados y revisiones manuales se atienden por política de reembolso y soporte.",
    refundPolicy: "Política de reembolso",
    support: "Soporte",
    legalHub: "Centro legal",
    terms: "Términos",
    refund: "Reembolso",
    contentRules:
      "No subas contenido ilegal, malicioso o que infrinja derechos.",
    factCapacity: "Capacidad por envío",
    factRetention: "Retención",
    factFileLimit: "Por archivo",
    factSignup: "Registro",
    factSignupNotRequired: "No hace falta",
    factSignupRequired: "Necesario",
    abuseDmca: "Abuso/DMCA",
    cookies: "Cookies",
    status: "Estado",
    deletion: "Eliminación",
    openApp: "Abrir app",
    launchDocuments: "Documentos de lanzamiento",
    legalNavigation: "Navegación legal",
    footerLegal: "Legal",
    footerTrust: "Confianza",
    footerSupport: "Soporte",
    lastUpdated: "Última actualización",
    supportRequests:
      "Solicitudes de cuenta, facturación, privacidad, DPA y derechos de datos",
    abuseRequests: "Abuso, malware, DMCA y solicitudes urgentes de retirada",
    supportContactLoading: "Contacto de soporte cargando",
    abuseContactLoading: "Contacto de abuso cargando",
    publicDocFooter:
      "Esta página refleja la arquitectura confirmada de beta pública y debe actualizarse cuando cambien proveedores, retención o flujos de solicitud.",
    storage: "Almacenamiento",
    file: "Archivo",
    retention: "Retención",
    subprocessors: "Subprocesadores",
    traffic: "Tráfico",
    activePastes: "pastes activos",
    activePastesLabel: "Pastes activos",
    planLimit: "límite del plan",
    storageOf: "de",
    sharedLinks: "Enlaces compartidos",
    pastesExposed: "pastes expuestos",
    attachmentsLabel: "Adjuntos",
    expiringIn24h: "vencen en 24 h",
    noUrgentExpiry: "Sin vencimientos urgentes",
    unavailable: "No disponible",
    openCheckout: "Abrir pago",
    paymentAddress: "dirección",
    webhook: "Webhook",
    accountActive: "Cuenta activa",
    deletionScheduled: "Eliminación programada",
    saveProfile: "Guardar perfil",
    linkedAccounts: "Cuentas vinculadas",
    noLinkedAccounts: "No hay proveedores externos vinculados.",
    unlinkGoogle: "Desvincular Google",
    oauthUnlinked: "Cuenta OAuth desvinculada",
    export: "Exportar",
    deleteRequest: "Solicitar eliminación",
    cancelDelete: "Cancelar eliminación",
    deleteNow: "Eliminar ahora",
    accountRights: "Derechos de cuenta",
    accountRightsBody:
      "Revisa exportación de datos, eliminación de cuenta, privacidad y soporte antes de enviar solicitudes sensibles.",
    dataExport: "exportación de datos",
    accountDeletion: "eliminación de cuenta",
    privacy: "privacidad",
    supportIntake: "entrada de soporte",
    reportTarget: "objetivo del reporte",
    reportReason: "motivo del reporte",
    auditQueuesCleanup: "Auditoría, colas y limpieza",
    adminTabConfiguration: "Configuración de la aplicación",
    adminControlPanel: "Panel de control",
    activeStorageUsage: "Almacenamiento usado",
    openReports: "Reportes abiertos",
    scanQueue: "Cola de escaneo",
    clientId: "ID de cliente",
    clientSecret: "Secreto de cliente",
    redirectUrl: "URL de retorno",
    webhookSecret: "Secreto de webhook",
    checkoutUrl: "URL de pago",
    merchantId: "ID de comercio",
    provider: "Proveedor",
    logMailer: "Solo registrar",
    scannerHeuristic: "Escaneo heurístico",
    configurationReady: "Configurado",
    configurationIncomplete: "Configuración incompleta",
    missingConfiguration: "Falta",
    providerTestOk: "Última prueba correcta",
    providerTestMissingConfig: "Prueba bloqueada por configuración faltante",
    providerTestSendFailed: "Falló el envío de prueba",
    alertCpuHigh: "Uso elevado de CPU de PasteBox",
    alertMemoryHigh: "Uso elevado de memoria de PasteBox",
    alertDiskHigh: "Uso elevado de disco de PasteBox",
    alertObjectStorageHigh: "Uso elevado del almacenamiento de objetos",
    alertScanFailuresHigh: "Fallos en cola de escaneo de PasteBox",
    alertFailedJobsHigh: "Trabajos fallidos de PasteBox",
    alertFailedMailsHigh: "Correos fallidos de PasteBox",
    alertOpenReportsHigh: "Reportes abiertos de PasteBox",
    maliciousAttachment: "Adjunto malicioso",
    statusQueued: "En cola",
    statusPending: "Pendiente",
    statusFailed: "Fallido",
    statusOpen: "Abierto",
    statusSent: "Enviado",
    statusSuppressed: "Suprimido",
    statusResolved: "Resuelto",
    statusDismissed: "Descartado",
    runtimeConfig: "Configuración runtime",
    processLogs: "Logs del proceso",
    logLevel: "Nivel de log",
    logLevelDebug: "Depuración",
    logLevelInfo: "Información",
    logLevelWarn: "Advertencia",
    logLevelError: "Error",
    resourcePanel: "Recursos",
    loadingSession: "Comprobando sesión…",
    openWorkspace: "Abrir espacio de trabajo",
    saving: "Guardando…",
    configSection: "Grupo de ajustes",
    planLimits: "Límites del plan",
    priceSettings: "Precios",
    choosePlan: "Elegir plan",
    choosePrice: "Elegir precio",
    savePlan: "Guardar este plan",
    savePrice: "Guardar este precio",
    periodMonthly: "Mensual",
    periodYearly: "Anual",

    planCatalog: "Planes y precios",
    providerStatus: "Estado de proveedores",
    manualReview: "Revisión manual",
    redemptionCodes: "Códigos",
    alertHistory: "Alertas",
    saveCatalog: "Guardar catálogo",
    catalogSaved: "Catálogo guardado",
    runtimeConfigSaved: "Configuración guardada",
    saveRuntimeConfig: "Guardar configuración",
    applicationConfig: "Configuración de la aplicación",
    applicationName: "Nombre de la aplicación",
    siteSettings: "Configuración del sitio",
    publicUrl: "URL pública",
    supportEmail: "Correo de soporte",
    abuseEmail: "Correo de abuso",
    corsOrigins: "Orígenes CORS permitidos",
    workerHeartbeatMaxAge: "Tiempo máximo del latido del worker",
    endpoint: "Endpoint",
    bucket: "Bucket",
    region: "Región",
    pathStyle: "Usar solicitudes de estilo ruta",
    accessKey: "Clave de acceso",
    secretKey: "Clave secreta",
    mailService: "Servicio de correo",
    smtpHost: "Servidor SMTP",
    smtpPort: "Puerto SMTP",
    username: "Usuario",
    fromEmail: "Correo remitente",
    fromName: "Nombre remitente",
    tlsMode: "Modo TLS",
    oauthAndCaptcha: "OAuth y protección contra abuso",
    turnstileSecretKey: "Clave secreta de Turnstile",
    verifyUrl: "URL de verificación",
    scannerAndNotifications: "Escaneo y notificaciones",
    scannerProvider: "Proveedor de escaneo",
    clamavAddress: "Dirección de ClamAV",
    scannerTimeout: "Tiempo límite de escaneo",
    telegramChatId: "ID de chat de Telegram",
    telegramBotToken: "Token del bot de Telegram",
    apiBaseUrl: "URL base de la API",
    paymentProviders: "Proveedores de pago",
    chain: "Red",
    port: "puerto",
    secretConfigured: "configurado",
    secretMissing: "sin configurar",
    secretKeepPlaceholder: "Déjalo vacío para conservar el valor actual",
    clearSecret: "Borrar secreto guardado",
    saveApplicationConfig: "Guardar configuración de la aplicación",
    managedConfigSaved: "Configuración de la aplicación guardada",
    guestUploads: "Subidas invitadas",
    requireTurnstile: "Requerir Turnstile",
    enabled: "Activado",
    disabled: "Desactivado",
    telegramAlerts: "Alertas Telegram",
    telegramDelivery: "Envío Telegram",
    silentAlert: "Alerta silenciosa",
    cooldownSeconds: "Segundos de pausa",
    cpuThreshold: "Umbral CPU %",
    memoryThreshold: "Umbral memoria %",
    diskThreshold: "Umbral disco %",
    objectStorageThreshold: "Umbral objetos",
    scanFailureThreshold: "Umbral escaneo",
    failedJobThreshold: "Umbral trabajos",
    failedMailThreshold: "Umbral correos",
    openReportThreshold: "Umbral reportes",
    sendTestAlert: "Enviar alerta",
    alertSent: "Alerta enviada",
    providerTested: "Proveedor probado",
    createRedemptionBatch: "Crear lote",
    redemptionBatchCreated: "Lote creado",
    redemptionBatchUpdated: "Lote actualizado",
    durationDays: "Días",
    quantity: "Cantidad",
    note: "Nota",
    objectStorage: "Objetos",
    cpu: "CPU",
    memory: "Memoria",
    disk: "Disco",
    activePasteLimitShort: "Límite activo",
    activeStorageLimit: "Almacenamiento activo",
    singleTextLimit: "Texto único",
    singleFileLimit: "Archivo único",
    singlePasteLimit: "Paste único",
    attachmentsPerPaste: "Adjuntos por paste",
    retentionSeconds: "Retención segundos",
    dailyUploadLimit: "Subida diaria",
    dailyShareDownloadLimit: "Descarga compartida diaria",
    period: "Periodo",
    currency: "Moneda",
    priceCents: "Precio centavos",
    visible: "Visible",
    purchase: "Compra",
    noRecentAlerts: "Sin alertas recientes",
    noManualWorkItems: "Sin revisión manual",
    runCleanup: "Ejecutar limpieza",
    runBillingReconcile: "Reconciliar facturación",
    users: "Usuarios",
    attachments: "Adjuntos",
    orders: "Pedidos",
    paid: "Marcar pagado",
    manualPaymentReason: "Motivo de pago manual",
    manualPaymentReasonPlaceholder: "Ticket de soporte o motivo de corrección",
    manualPaymentReasonRequired:
      "Ingresa un motivo de soporte antes de marcar como pagado",
    queues: "Colas",
    scanFailures: "Fallos de escaneo",
    cleanupJobs: "Trabajos de limpieza",
    cleanupFailures: "Fallos de limpieza",
    failedJobs: "Trabajos fallidos",
    queuedMails: "Correos en cola",
    failedMails: "Correos fallidos",
    attempts: "intentos",
    deleteFailures: "Fallos de eliminación",
    resolve: "Resolver",
    dismiss: "Descartar",
    retry: "Reintentar",
    release: "Liberar",
    freeze: "Congelar",
    frozen: "congelado",
    webhooks: "Webhooks",
    replay: "Reintentar",
    copy: "Copiar",
    accountReady: "Cuenta lista",
    signedIn: "Sesión iniciada",
    signedInWithGoogle: "Sesión iniciada con Google",
    verificationIssued: "Verificación emitida",
    emailVerified: "Correo verificado",
    emailVerifiedLogin: "Correo verificado. Inicia sesión para continuar.",
    emailVerifiedDifferentAccount:
      "Correo verificado para otra cuenta. Cierra sesión antes de cambiar.",
    passwordResetLinkReady:
      "Ingresa una contraseña nueva para terminar el restablecimiento.",
    signedOut: "Sesión cerrada",
    allSessionsSignedOut: "Todas las sesiones cerradas",
    logoutAllDevices: "Cerrar sesiones en todos los dispositivos",
    logoutAllDevicesDescription:
      "Cierra cada sesión activa de PasteBox en este navegador y en otros dispositivos. Tendrás que iniciar sesión de nuevo en todas partes.",
    passwordResetIssued:
      "Correo de restablecimiento enviado. Ábrelo para continuar.",
    passwordUpdated: "Contraseña actualizada",
    reportSubmitted: "Reporte enviado",
    pasteCreated: "Paste creado",
    attachmentUploaded: "Adjunto subido",
    shareLinkCreated: "Enlace creado",
    shareOpened: "Enlace abierto",
    pasteUpdated: "Paste actualizado",
    pinUpdated: "Fijado actualizado",
    favoriteUpdated: "Favorito actualizado",
    expirationExtended: "Vencimiento extendido",
    orderCreated: "Pedido creado",
    exportGenerated: "Exportación generada",
    deletionCanceled: "Eliminación cancelada",
    accountDeleted: "Cuenta eliminada",
    profileUpdated: "Perfil actualizado",
    cleanupCompleted: "Limpieza completada",
    billingReconciled: "Facturación reconciliada",
    scanRetried: "Escaneo reintentado",
    attachmentFrozen: "Adjunto congelado",
    attachmentReleased: "Adjunto liberado",
    shareRevoked: "Enlace revocado",
    orderMarkedPaid: "Pedido marcado como pagado",
    webhookProcessed: "Webhook procesado",
    webhookReplayed: "Webhook reintentado",
    reportUpdated: "Reporte actualizado",
    pasteDeleted: "Paste eliminado",
    requestFailed: "Solicitud fallida",
    unpinPaste: "Desfijar paste",
    pinPaste: "Fijar paste",
    removeFavorite: "Quitar favorito",
    favoritePaste: "Marcar favorito",
    copyText: "Copiar texto",
    extendPaste: "Extender paste",
    deletePaste: "Eliminar paste",
  },
};

function copyFor(language?: string) {
  const locale = localeFor(language);
  return (key: string) => copy[locale][key] ?? copy.en[key] ?? key;
}

type Translate = (key: string) => string;

const adminMetricCopyKeys: Record<string, string> = {
  activePastes: "activePastesLabel",
  activeStorageBytes: "activeStorageUsage",
  cleanupQueueDepth: "cleanupJobs",
  failedJobQueueDepth: "failedJobs",
  orders: "orders",
  reportsOpen: "openReports",
  scanFailureQueueDepth: "scanFailures",
  scanQueueDepth: "scanQueue",
  users: "users",
  webhookEvents: "webhooks",
};

const providerCopyKeys: Record<string, string> = {
  mailer: "mailService",
  log: "logMailer",
  google: "google",
  github: "github",
  cloudflare_turnstile: "turnstileChallenge",
  turnstile: "turnstileChallenge",
  s3: "objectStorage",
};

const providerBrandLabels: Record<string, string> = {
  smtp: "SMTP",
  telegram: "Telegram",
  stripe: "Stripe",
  epusdt: "Epusdt",
};

const providerFieldCopyKeys: Record<string, string> = {
  "mailer.provider": "provider",
  "smtp.host": "smtpHost",
  "smtp.from_email": "fromEmail",
  "smtp.password": "password",
  "google.client_id": "clientId",
  "google.client_secret": "clientSecret",
  "google.redirect_url": "redirectUrl",
  "github.client_id": "clientId",
  "github.client_secret": "clientSecret",
  "github.redirect_url": "redirectUrl",
  "turnstile.site_key": "turnstileSiteKey",
  "turnstile.secret_key": "turnstileSecretKey",
  "telegram.bot_token": "telegramBotToken",
  "telegram.chat_id": "telegramChatId",
  "s3.endpoint": "endpoint",
  "s3.bucket": "bucket",
  "s3.access_key": "accessKey",
  "s3.secret_key": "secretKey",
  "stripe.enabled": "enabled",
  "stripe.webhook_secret": "webhookSecret",
  "stripe.checkout_url_template": "checkoutUrl",
  "epusdt.enabled": "enabled",
  "epusdt.pid": "merchantId",
  "epusdt.secret_key": "secretKey",
  "epusdt.checkout_url_template": "checkoutUrl",
  "epusdt.address": "paymentAddress",
};

const adminStatusCopyKeys: Record<string, string> = {
  queued: "statusQueued",
  pending: "statusPending",
  failed: "statusFailed",
  open: "statusOpen",
  sent: "statusSent",
  suppressed: "statusSuppressed",
  resolved: "statusResolved",
  dismissed: "statusDismissed",
  frozen: "frozen",
  active: "active",
};

const manualWorkKindCopyKeys: Record<string, string> = {
  malicious_attachment: "maliciousAttachment",
  failed_job: "failedJobs",
  scan_failure: "scanFailures",
  failed_mail: "failedMails",
  open_report: "openReports",
};

const runtimeAlertCopyKeys: Record<string, string> = {
  cpu_high: "alertCpuHigh",
  memory_high: "alertMemoryHigh",
  disk_high: "alertDiskHigh",
  object_storage_high: "alertObjectStorageHigh",
  scan_failures_high: "alertScanFailuresHigh",
  failed_jobs_high: "alertFailedJobsHigh",
  failed_mails_high: "alertFailedMailsHigh",
  reports_open_high: "alertOpenReportsHigh",
};

function humanizeIdentifier(value: string): string {
  const normalized = value
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/[._-]+/g, " ")
    .trim();
  return normalized
    ? normalized.charAt(0).toUpperCase() + normalized.slice(1)
    : value;
}

function translatedIdentifier(
  value: string,
  keys: Record<string, string>,
  t: Translate,
): string {
  const key = keys[value] ?? keys[value.toLowerCase()];
  return key ? t(key) : humanizeIdentifier(value);
}

function adminMetricLabel(metric: string, t: Translate): string {
  return translatedIdentifier(metric, adminMetricCopyKeys, t);
}

function adminMetricValue(metric: string, value: unknown): string {
  if (metric === "activeStorageBytes" && typeof value === "number") {
    return formatBytes(value);
  }
  return String(value);
}

function providerLabel(provider: string, t: Translate): string {
  const brand = providerBrandLabels[provider.toLowerCase()];
  if (brand) return brand;
  return translatedIdentifier(provider, providerCopyKeys, t);
}

function providerFieldLabel(field: string, t: Translate): string {
  return translatedIdentifier(field, providerFieldCopyKeys, t);
}

function providerTestStatusLabel(status: string, t: Translate): string {
  if (status === "ok") return t("providerTestOk");
  if (status === "send_failed") return t("providerTestSendFailed");
  if (status.startsWith("missing_")) return t("providerTestMissingConfig");
  return humanizeIdentifier(status);
}

function adminStatusLabel(status: string, t: Translate): string {
  return translatedIdentifier(status, adminStatusCopyKeys, t);
}

function manualWorkKindLabel(kind: string, t: Translate): string {
  return translatedIdentifier(kind, manualWorkKindCopyKeys, t);
}

function runtimeAlertMessage(alert: AlertEvent, t: Translate): string {
  const key = runtimeAlertCopyKeys[alert.fingerprint];
  if (!key) return alert.message;
  const separator = alert.message.indexOf(":");
  const value = separator >= 0 ? alert.message.slice(separator + 1).trim() : "";
  return value ? `${t(key)}: ${value}` : t(key);
}

const sizeUnits: SizeUnit[] = ["KB", "MB", "GB"];
const timeUnits: TimeUnit[] = ["seconds", "minutes", "hours", "days"];

function sizeUnitFactor(unit: SizeUnit): number {
  switch (unit) {
    case "GB":
      return 1024 * 1024 * 1024;
    case "MB":
      return 1024 * 1024;
    case "KB":
    default:
      return 1024;
  }
}

function timeUnitFactor(unit: TimeUnit): number {
  switch (unit) {
    case "days":
      return 24 * 60 * 60;
    case "hours":
      return 60 * 60;
    case "minutes":
      return 60;
    case "seconds":
    default:
      return 1;
  }
}

function preferredSizeUnit(bytes: number): SizeUnit {
  const gb = sizeUnitFactor("GB");
  const mb = sizeUnitFactor("MB");
  if (bytes >= gb && bytes % gb === 0) return "GB";
  if (bytes >= mb && bytes % mb === 0) return "MB";
  return "KB";
}

function preferredTimeUnit(seconds: number): TimeUnit {
  const day = timeUnitFactor("days");
  const hour = timeUnitFactor("hours");
  const minute = timeUnitFactor("minutes");
  if (seconds >= day && seconds % day === 0) return "days";
  if (seconds >= hour && seconds % hour === 0) return "hours";
  if (seconds >= minute && seconds % minute === 0) return "minutes";
  return "seconds";
}

function formatUnitValue(value: number, factor: number): string {
  const converted = value / factor;
  if (Number.isInteger(converted)) return String(converted);
  return converted.toFixed(2).replace(/\.?0+$/, "");
}

function AdminSizeField({
  label,
  valueBytes,
  minBytes,
  unit,
  t,
  onChangeBytes,
  onUnitChange,
}: {
  label: string;
  valueBytes: number;
  minBytes: number;
  unit: SizeUnit;
  t: (key: string) => string;
  onChangeBytes: (value: number) => void;
  onUnitChange: (unit: SizeUnit) => void;
}) {
  const factor = sizeUnitFactor(unit);
  const min = minBytes > 0 ? minBytes / factor : 0;
  return (
    <label className="field-row field-row--unit">
      <span>{label}</span>
      <div className="unit-input">
        <input
          min={min}
          step={unit === "KB" ? 1 : 0.01}
          type="number"
          value={formatUnitValue(valueBytes, factor)}
          onChange={(event) =>
            onChangeBytes(Math.round(Number(event.target.value) * factor))
          }
        />
        <select
          aria-label={`${label} ${t("unit")}`}
          value={unit}
          onChange={(event) => onUnitChange(event.target.value as SizeUnit)}
        >
          {sizeUnits.map((item) => (
            <option key={item} value={item}>
              {t(`unit${item}`)}
            </option>
          ))}
        </select>
      </div>
    </label>
  );
}

function AdminTimeField({
  label,
  valueSeconds,
  minSeconds,
  unit,
  t,
  onChangeSeconds,
  onUnitChange,
}: {
  label: string;
  valueSeconds: number;
  minSeconds: number;
  unit: TimeUnit;
  t: (key: string) => string;
  onChangeSeconds: (value: number) => void;
  onUnitChange: (unit: TimeUnit) => void;
}) {
  const factor = timeUnitFactor(unit);
  const min = minSeconds > 0 ? minSeconds / factor : 0;
  return (
    <label className="field-row field-row--unit">
      <span>{label}</span>
      <div className="unit-input">
        <input
          min={min}
          step={unit === "seconds" ? 1 : 0.1}
          type="number"
          value={formatUnitValue(valueSeconds, factor)}
          onChange={(event) =>
            onChangeSeconds(Math.round(Number(event.target.value) * factor))
          }
        />
        <select
          aria-label={`${label} ${t("unit")}`}
          value={unit}
          onChange={(event) => onUnitChange(event.target.value as TimeUnit)}
        >
          {timeUnits.map((item) => (
            <option key={item} value={item}>
              {t(`unit${item}`)}
            </option>
          ))}
        </select>
      </div>
    </label>
  );
}

function AdminNumberField({
  label,
  value,
  min,
  step,
  unitLabel,
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  step?: number;
  unitLabel: string;
  onChange: (value: number) => void;
}) {
  return (
    <label className="field-row field-row--unit">
      <span>{label}</span>
      <div className="unit-input unit-input--suffix">
        <input
          min={min}
          step={step}
          type="number"
          value={value}
          onChange={(event) => onChange(Number(event.target.value))}
        />
        <span className="field-unit-label">{unitLabel}</span>
      </div>
    </label>
  );
}

type SaveResult = { text: string; error: boolean };

function SaveFeedback({ result }: { result?: SaveResult }) {
  return result ? (
    <p
      className={`save-feedback${result.error ? " save-feedback--error" : ""}`}
      role={result.error ? "alert" : "status"}
    >
      {result.text}
    </p>
  ) : null;
}

function ManagedTextField({
  label,
  value,
  type = "text",
  onChange,
}: {
  label: string;
  value: string;
  type?: "text" | "url" | "email";
  onChange: (value: string) => void;
}) {
  return (
    <label className="field-row">
      <span>{label}</span>
      <input
        type={type}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </label>
  );
}

function ManagedSecretField({
  label,
  name,
  configured,
  draft,
  t,
  onChange,
}: {
  label: string;
  name: keyof ManagedSecretPatch;
  configured: boolean;
  draft: ManagedSecretPatch;
  t: (key: string) => string;
  onChange: (name: keyof ManagedSecretPatch, value: string | undefined) => void;
}) {
  return (
    <div className="field-row managed-secret-field">
      <span>
        {label} · {configured ? t("secretConfigured") : t("secretMissing")}
      </span>
      <div className="managed-secret-input">
        <input
          autoComplete="new-password"
          type="password"
          value={draft[name] ?? ""}
          placeholder={configured ? t("secretKeepPlaceholder") : ""}
          aria-label={label}
          onChange={(event) => onChange(name, event.target.value || undefined)}
        />
        <button
          aria-label={t("clearSecret")}
          title={t("clearSecret")}
          type="button"
          onClick={() => onChange(name, "")}
        >
          <Trash2 size={16} aria-hidden="true" />
        </button>
      </div>
    </div>
  );
}

const managedGroups: Array<{
  id: string;
  label: string;
  fields: (keyof ManagedConfig)[];
  secrets: (keyof ManagedSecretPatch)[];
}> = [
  {
    id: "site",
    label: "siteSettings",
    fields: ["site", "workerHeartbeatMaxAgeSeconds"],
    secrets: [],
  },
  {
    id: "storage",
    label: "objectStorage",
    fields: ["s3"],
    secrets: ["s3AccessKey", "s3SecretKey"],
  },
  {
    id: "mail",
    label: "mailService",
    fields: ["mailerProvider", "smtp", "devAuthTokens"],
    secrets: ["smtpPassword"],
  },
  {
    id: "auth",
    label: "oauthAndCaptcha",
    fields: ["googleOAuth", "githubOAuth", "turnstile"],
    secrets: ["googleClientSecret", "githubClientSecret", "turnstileSecretKey"],
  },
  {
    id: "scanner",
    label: "scannerAndNotifications",
    fields: ["scanner", "telegram"],
    secrets: ["telegramBotToken"],
  },
  {
    id: "payments",
    label: "paymentProviders",
    fields: ["stripeEnabled", "stripe", "epusdtEnabled", "epusdt"],
    secrets: ["stripeWebhookSecret", "epusdtSecretKey"],
  },
];

function ManagedConfigEditor({
  view,
  secretDraft,
  t,
  onChange,
  onSecretChange,
  onSave,
  saveResults,
  busy,
}: {
  view: ManagedConfigView;
  secretDraft: ManagedSecretPatch;
  t: (key: string) => string;
  onChange: (config: ManagedConfig) => void;
  onSecretChange: (
    name: keyof ManagedSecretPatch,
    value: string | undefined,
  ) => void;
  onSave: (group: (typeof managedGroups)[number]) => void;
  saveResults: Record<string, SaveResult>;
  busy: boolean;
}) {
  const cfg = view.config;
  const [section, setSection] = useState("site");
  const group =
    managedGroups.find((entry) => entry.id === section) ?? managedGroups[0];
  const patch = (value: Partial<ManagedConfig>) =>
    onChange({ ...cfg, ...value });
  const secret = (label: string, name: keyof ManagedSecretPatch) => (
    <ManagedSecretField
      configured={view.secrets[name]}
      draft={secretDraft}
      label={label}
      name={name}
      t={t}
      onChange={onSecretChange}
    />
  );

  return (
    <section className="admin-section admin-section--wide managed-config-editor">
      <h3>{t("applicationConfig")}</h3>
      <label className="field-row config-section-select">
        <span>{t("configSection")}</span>
        <select
          value={section}
          onChange={(event) => setSection(event.target.value)}
        >
          {managedGroups.map((entry) => (
            <option key={entry.id} value={entry.id}>
              {t(entry.label)}
            </option>
          ))}
        </select>
      </label>
      <div className="managed-config-grid">
        <article className="list-card" hidden={section !== "site"}>
          <strong>{t("siteSettings")}</strong>
          <div className="form-grid">
            <ManagedTextField
              label={t("applicationName")}
              value={cfg.site.appName}
              onChange={(appName) => patch({ site: { ...cfg.site, appName } })}
            />
            <ManagedTextField
              label={t("publicUrl")}
              type="url"
              value={cfg.site.publicUrl}
              onChange={(publicUrl) =>
                patch({ site: { ...cfg.site, publicUrl } })
              }
            />
            <ManagedTextField
              label={t("supportEmail")}
              type="email"
              value={cfg.site.supportEmail}
              onChange={(supportEmail) =>
                patch({ site: { ...cfg.site, supportEmail } })
              }
            />
            <ManagedTextField
              label={t("abuseEmail")}
              type="email"
              value={cfg.site.abuseEmail}
              onChange={(abuseEmail) =>
                patch({ site: { ...cfg.site, abuseEmail } })
              }
            />
            <label className="field-row field-row--wide">
              <span>{t("corsOrigins")}</span>
              <textarea
                value={cfg.site.corsAllowedOrigins.join("\n")}
                onChange={(event) =>
                  patch({
                    site: {
                      ...cfg.site,
                      corsAllowedOrigins: event.target.value
                        .split(/[\n,]+/)
                        .map((value) => value.trim())
                        .filter(Boolean),
                    },
                  })
                }
              />
            </label>
            <AdminNumberField
              label={t("workerHeartbeatMaxAge")}
              min={1}
              unitLabel={t("unitseconds")}
              value={cfg.workerHeartbeatMaxAgeSeconds}
              onChange={(workerHeartbeatMaxAgeSeconds) =>
                patch({ workerHeartbeatMaxAgeSeconds })
              }
            />
          </div>
        </article>

        <article className="list-card" hidden={section !== "storage"}>
          <strong>{t("objectStorage")}</strong>
          <div className="form-grid">
            <ManagedTextField
              label={t("endpoint")}
              type="url"
              value={cfg.s3.endpoint}
              onChange={(endpoint) => patch({ s3: { ...cfg.s3, endpoint } })}
            />
            <ManagedTextField
              label={t("bucket")}
              value={cfg.s3.bucket}
              onChange={(bucket) => patch({ s3: { ...cfg.s3, bucket } })}
            />
            <ManagedTextField
              label={t("region")}
              value={cfg.s3.region}
              onChange={(region) => patch({ s3: { ...cfg.s3, region } })}
            />
            <label className="check-row">
              <input
                checked={cfg.s3.usePathStyle}
                type="checkbox"
                onChange={(event) =>
                  patch({
                    s3: { ...cfg.s3, usePathStyle: event.target.checked },
                  })
                }
              />
              {t("pathStyle")}
            </label>
            {secret(t("accessKey"), "s3AccessKey")}
            {secret(t("secretKey"), "s3SecretKey")}
          </div>
        </article>

        <article className="list-card" hidden={section !== "mail"}>
          <strong>{t("mailService")}</strong>
          <div className="form-grid">
            <label className="field-row">
              <span>{t("provider")}</span>
              <select
                value={cfg.mailerProvider}
                onChange={(event) =>
                  patch({ mailerProvider: event.target.value })
                }
              >
                <option value="log">{t("logMailer")}</option>
                <option value="smtp">SMTP</option>
              </select>
            </label>
            <ManagedTextField
              label={t("smtpHost")}
              value={cfg.smtp.host}
              onChange={(host) => patch({ smtp: { ...cfg.smtp, host } })}
            />
            <AdminNumberField
              label={t("smtpPort")}
              min={1}
              unitLabel={t("port")}
              value={cfg.smtp.port}
              onChange={(port) => patch({ smtp: { ...cfg.smtp, port } })}
            />
            <ManagedTextField
              label={t("username")}
              value={cfg.smtp.username}
              onChange={(username) =>
                patch({ smtp: { ...cfg.smtp, username } })
              }
            />
            {secret(t("password"), "smtpPassword")}
            <ManagedTextField
              label={t("fromEmail")}
              type="email"
              value={cfg.smtp.fromEmail}
              onChange={(fromEmail) =>
                patch({ smtp: { ...cfg.smtp, fromEmail } })
              }
            />
            <ManagedTextField
              label={t("fromName")}
              value={cfg.smtp.fromName}
              onChange={(fromName) =>
                patch({ smtp: { ...cfg.smtp, fromName } })
              }
            />
            <label className="field-row">
              <span>{t("tlsMode")}</span>
              <select
                value={cfg.smtp.tlsMode}
                onChange={(event) =>
                  patch({
                    smtp: { ...cfg.smtp, tlsMode: event.target.value },
                  })
                }
              >
                <option value="none">{t("disabled")}</option>
                <option value="starttls">STARTTLS</option>
                <option value="tls">TLS</option>
              </select>
            </label>
          </div>
        </article>

        <article className="list-card" hidden={section !== "auth"}>
          <strong>{t("oauthAndCaptcha")}</strong>
          <div className="form-grid">
            <ManagedTextField
              label={`${t("google")} ${t("clientId")}`}
              value={cfg.googleOAuth.clientId}
              onChange={(clientId) =>
                patch({ googleOAuth: { ...cfg.googleOAuth, clientId } })
              }
            />
            {secret(
              `${t("google")} ${t("clientSecret")}`,
              "googleClientSecret",
            )}
            <ManagedTextField
              label={`${t("google")} ${t("redirectUrl")}`}
              type="url"
              value={cfg.googleOAuth.redirectUrl}
              onChange={(redirectUrl) =>
                patch({ googleOAuth: { ...cfg.googleOAuth, redirectUrl } })
              }
            />
            <ManagedTextField
              label={`${t("github")} ${t("clientId")}`}
              value={cfg.githubOAuth.clientId}
              onChange={(clientId) =>
                patch({ githubOAuth: { ...cfg.githubOAuth, clientId } })
              }
            />
            {secret(
              `${t("github")} ${t("clientSecret")}`,
              "githubClientSecret",
            )}
            <ManagedTextField
              label={`${t("github")} ${t("redirectUrl")}`}
              type="url"
              value={cfg.githubOAuth.redirectUrl}
              onChange={(redirectUrl) =>
                patch({ githubOAuth: { ...cfg.githubOAuth, redirectUrl } })
              }
            />
            <ManagedTextField
              label={t("turnstileSiteKey")}
              value={cfg.turnstile.siteKey}
              onChange={(siteKey) =>
                patch({ turnstile: { ...cfg.turnstile, siteKey } })
              }
            />
            {secret(t("turnstileSecretKey"), "turnstileSecretKey")}
            <ManagedTextField
              label={t("verifyUrl")}
              type="url"
              value={cfg.turnstile.verifyUrl}
              onChange={(verifyUrl) =>
                patch({ turnstile: { ...cfg.turnstile, verifyUrl } })
              }
            />
          </div>
        </article>

        <article className="list-card" hidden={section !== "scanner"}>
          <strong>{t("scannerAndNotifications")}</strong>
          <div className="form-grid">
            <label className="field-row">
              <span>{t("scannerProvider")}</span>
              <select
                value={cfg.scanner.provider}
                onChange={(event) =>
                  patch({
                    scanner: { ...cfg.scanner, provider: event.target.value },
                  })
                }
              >
                <option value="heuristic">{t("scannerHeuristic")}</option>
                <option value="clamav">ClamAV</option>
              </select>
            </label>
            <ManagedTextField
              label={t("clamavAddress")}
              value={cfg.scanner.clamav.addr}
              onChange={(addr) =>
                patch({
                  scanner: {
                    ...cfg.scanner,
                    clamav: { ...cfg.scanner.clamav, addr },
                  },
                })
              }
            />
            <AdminNumberField
              label={t("scannerTimeout")}
              min={1}
              unitLabel={t("unitseconds")}
              value={cfg.scanner.clamav.timeout}
              onChange={(timeout) =>
                patch({
                  scanner: {
                    ...cfg.scanner,
                    clamav: { ...cfg.scanner.clamav, timeout },
                  },
                })
              }
            />
            <ManagedTextField
              label={t("telegramChatId")}
              value={cfg.telegram.chatId}
              onChange={(chatId) =>
                patch({ telegram: { ...cfg.telegram, chatId } })
              }
            />
            {secret(t("telegramBotToken"), "telegramBotToken")}
            <ManagedTextField
              label={t("apiBaseUrl")}
              type="url"
              value={cfg.telegram.apiBaseUrl}
              onChange={(apiBaseUrl) =>
                patch({ telegram: { ...cfg.telegram, apiBaseUrl } })
              }
            />
          </div>
        </article>

        <article className="list-card" hidden={section !== "payments"}>
          <strong>{t("paymentProviders")}</strong>
          <div className="form-grid">
            <label className="check-row">
              <input
                checked={cfg.stripeEnabled}
                type="checkbox"
                onChange={(event) =>
                  patch({ stripeEnabled: event.target.checked })
                }
              />
              Stripe
            </label>
            {secret(`Stripe ${t("webhookSecret")}`, "stripeWebhookSecret")}
            <label className="field-row field-row--wide">
              <span>Stripe {t("checkoutUrl")}</span>
              <textarea
                value={cfg.stripe.checkoutUrlTemplate}
                onChange={(event) =>
                  patch({
                    stripe: {
                      ...cfg.stripe,
                      checkoutUrlTemplate: event.target.value,
                    },
                  })
                }
              />
            </label>
            <label className="check-row">
              <input
                checked={cfg.epusdtEnabled}
                type="checkbox"
                onChange={(event) =>
                  patch({ epusdtEnabled: event.target.checked })
                }
              />
              Epusdt
            </label>
            <ManagedTextField
              label={`Epusdt ${t("merchantId")}`}
              value={cfg.epusdt.pid}
              onChange={(pid) => patch({ epusdt: { ...cfg.epusdt, pid } })}
            />
            {secret(`Epusdt ${t("secretKey")}`, "epusdtSecretKey")}
            <ManagedTextField
              label={t("paymentAddress")}
              value={cfg.epusdt.address}
              onChange={(address) =>
                patch({ epusdt: { ...cfg.epusdt, address } })
              }
            />
            <ManagedTextField
              label={t("chain")}
              value={cfg.epusdt.chain}
              onChange={(chain) => patch({ epusdt: { ...cfg.epusdt, chain } })}
            />
            <label className="field-row field-row--wide">
              <span>Epusdt {t("checkoutUrl")}</span>
              <textarea
                value={cfg.epusdt.checkoutUrlTemplate}
                onChange={(event) =>
                  patch({
                    epusdt: {
                      ...cfg.epusdt,
                      checkoutUrlTemplate: event.target.value,
                    },
                  })
                }
              />
            </label>
          </div>
        </article>
      </div>
      <button type="button" onClick={() => onSave(group)} disabled={busy}>
        <ShieldCheck size={16} aria-hidden="true" />
        {t("saveApplicationConfig")}
      </button>
      <SaveFeedback result={saveResults[`managed-${group.id}`]} />
    </section>
  );
}

declare global {
  interface Window {
    turnstile?: {
      render: (
        element: HTMLElement,
        options: {
          sitekey: string;
          callback: (token: string) => void;
          "expired-callback"?: () => void;
          "error-callback"?: () => void;
          language?: string;
        },
      ) => string;
      remove: (widgetId: string) => void;
    };
  }
}

function TurnstileWidget({
  siteKey,
  locale,
  onToken,
}: {
  siteKey: string;
  locale: Locale;
  onToken: (token: string) => void;
}) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const widgetRef = useRef<string | null>(null);
  const onTokenRef = useRef(onToken);

  useEffect(() => {
    onTokenRef.current = onToken;
  }, [onToken]);

  useEffect(() => {
    if (!siteKey || !containerRef.current) return;
    let canceled = false;
    const render = () => {
      if (canceled || !window.turnstile || !containerRef.current) return;
      if (widgetRef.current) {
        window.turnstile.remove(widgetRef.current);
      }
      widgetRef.current = window.turnstile.render(containerRef.current, {
        sitekey: siteKey,
        language: locale.toLowerCase(),
        callback: (token) => onTokenRef.current(token),
        "expired-callback": () => onTokenRef.current(""),
        "error-callback": () => onTokenRef.current(""),
      });
    };

    if (window.turnstile) {
      render();
    } else {
      const script = document.createElement("script");
      script.src = "https://challenges.cloudflare.com/turnstile/v0/api.js";
      script.async = true;
      script.defer = true;
      script.onload = render;
      document.head.appendChild(script);
    }

    return () => {
      canceled = true;
      if (widgetRef.current && window.turnstile) {
        window.turnstile.remove(widgetRef.current);
        widgetRef.current = null;
      }
    };
  }, [locale, siteKey]);

  return <div className="turnstile-box" ref={containerRef} />;
}

function splitEmailForRegistration(
  email: string,
  domains: string[],
): { local: string; domain: string } {
  const [localPart = "", domainPart = ""] = email.split("@");
  const fallbackDomain = domains[0] ?? "";
  const domain = domains.includes(domainPart) ? domainPart : fallbackDomain;
  return { local: localPart, domain };
}

function buildRegistrationEmail(local: string, domain: string): string {
  const cleanLocal = local.trim().replace(/@/g, "");
  return domain ? `${cleanLocal}@${domain}` : cleanLocal;
}

function domainListText(domains: string[]): string {
  return domains.join(", ");
}

function parseDomainList(value: string): string[] {
  return Array.from(
    new Set(
      value
        .split(/[\s,;]+/)
        .map((item) => item.trim().toLowerCase().replace(/^@+/, ""))
        .filter(Boolean),
    ),
  );
}

function landingContentFor(locale: Locale): LandingContent {
  if (locale === "zh-TW") {
    return {
      navProduct: "產品",
      navSecurity: "安全",
      navPricing: "方案",
      eyebrow: "跨裝置 · 檔案中轉",
      title: "傳檔案、取檔案，換個裝置接著用。",
      subtitle:
        "打開就能傳檔案：選擇或拖入檔案，上傳完成後產生分享連結，接收方打開連結即可下載。文字和圖片分享照舊，登入後還有歷史記錄和更大容量。",
      primaryCta: "免費註冊",
      secondaryCta: "登入",
      workspaceLabel: "PasteBox 工作台",
      workspaceTitle: "為什麼選 PasteBox",
      workspaceBody: "傳得更快，連結更可控，檔案更安心。",
      features: [
        {
          title: "即開即用",
          body: "不用下載用戶端，瀏覽器打開就能貼上分享，訪客也能先把流程跑一遍。",
          stat: "0 安裝",
        },
        {
          title: "分享可控",
          body: "每條連結都能設密碼、限訪問和下載次數、設到期時間，發錯了隨時一鍵收回。",
          stat: "可撤銷",
        },
        {
          title: "檔案更安心",
          body: "附件會保留病毒掃描狀態，公開下載前風險一目了然。",
          stat: "已掃描",
        },
      ],
      steps: [
        { title: "選檔案", body: "預設檔案模式，選擇或拖入要傳送的檔案。" },
        { title: "產生連結", body: "全部檔案上傳成功後產生分享連結和有效期。" },
        { title: "對方取件", body: "接收方打開連結查看檔案清單並逐個下載。" },
      ],
      ctaTitle: "現在就把第一個檔案傳出去。",
      ctaBody: "免費註冊，立刻拿到歷史記錄、更大容量和完整的分享控制。",
      ctaBadges: ["免費開始", "免安裝", "可隨時撤銷", "附件已掃描"],
    };
  }

  if (locale === "zh-CN") {
    return {
      navProduct: "产品",
      navSecurity: "安全",
      navPricing: "套餐",
      eyebrow: "跨设备 · 文件中转",
      title: "发文件、取文件，换个设备接着用。",
      subtitle:
        "打开就能发文件：选择或拖入文件，上传完成后生成分享链接，接收方打开链接即可下载。文本和图片分享照旧，登录后还有历史记录和更大容量。",
      primaryCta: "免费注册",
      secondaryCta: "登录",
      workspaceLabel: "PasteBox 工作台",
      workspaceTitle: "为什么选 PasteBox",
      workspaceBody: "传得更快，链接更可控，文件更安心。",
      features: [
        {
          title: "即开即用",
          body: "不用下载客户端，浏览器打开就能粘贴分享，游客也能先把流程跑一遍。",
          stat: "0 安装",
        },
        {
          title: "分享可控",
          body: "每条链接都能设密码、限访问和下载次数、设到期时间，发错了随时一键收回。",
          stat: "可撤销",
        },
        {
          title: "文件更安心",
          body: "附件会保留病毒扫描状态，公开下载前风险一目了然。",
          stat: "已扫描",
        },
      ],
      steps: [
        { title: "选文件", body: "默认文件模式，选择或拖入要发送的文件。" },
        { title: "生成链接", body: "全部文件上传成功后生成分享链接和有效期。" },
        { title: "对方取件", body: "接收方打开链接查看文件清单并逐个下载。" },
      ],
      ctaTitle: "现在就把第一个文件发出去。",
      ctaBody: "免费注册，立刻拿到历史记录、更大容量和完整的分享控制。",
      ctaBadges: ["免费开始", "免安装", "可随时撤销", "附件已扫描"],
    };
  }

  if (locale === "es") {
    return {
      navProduct: "Producto",
      navSecurity: "Seguridad",
      navPricing: "Planes",
      eyebrow: "Transferencia de archivos entre dispositivos",
      title: "Envía un archivo y recógelo en otro dispositivo.",
      subtitle:
        "Abre y envía: elige o arrastra un archivo y el enlace de descarga aparece cuando termina la subida. El texto y las imágenes siguen funcionando igual, y al iniciar sesión tienes historial y más capacidad.",
      primaryCta: "Registrarse gratis",
      secondaryCta: "Iniciar sesión",
      workspaceLabel: "Escritorio PasteBox",
      workspaceTitle: "Por qué PasteBox",
      workspaceBody:
        "Transferencias más rápidas, enlaces más controlados y archivos más seguros.",
      features: [
        {
          title: "Listo al instante",
          body: "Sin cliente que instalar: abre el navegador y comparte. Los invitados también pueden probar todo el flujo.",
          stat: "0 instalar",
        },
        {
          title: "Enlaces controlados",
          body: "Cada enlace admite contraseña, límite de visitas y descargas y caducidad. Revócalo con un clic si te equivocas.",
          stat: "revocable",
        },
        {
          title: "Archivos más seguros",
          body: "Los adjuntos conservan el estado de escaneo antivirus para ver el riesgo antes de cualquier descarga pública.",
          stat: "escaneado",
        },
      ],
      steps: [
        {
          title: "Elige",
          body: "Modo archivo por defecto: elige o arrastra lo que quieres enviar.",
        },
        {
          title: "Enlace",
          body: "Cuando todas las subidas terminan se crean el enlace y su caducidad.",
        },
        {
          title: "Recoge",
          body: "Quien lo recibe abre el enlace, revisa la lista y descarga cada archivo.",
        },
      ],
      ctaTitle: "Envía tu primer archivo ahora.",
      ctaBody:
        "Regístrate gratis y obtén historial, más capacidad y control total de enlaces al instante.",
      ctaBadges: [
        "Gratis para empezar",
        "Sin instalar",
        "Revocable",
        "Adjuntos escaneados",
      ],
    };
  }

  return {
    navProduct: "Product",
    navSecurity: "Security",
    navPricing: "Pricing",
    eyebrow: "Cross-device file transfer",
    title: "Send a file here, pick it up anywhere.",
    subtitle:
      "Open and send: choose or drop a file and the download link appears once every upload finishes. Text and image sharing still work as before, and signing in adds history and bigger limits.",
    primaryCta: "Register free",
    secondaryCta: "Login",
    workspaceLabel: "PasteBox workspace",
    workspaceTitle: "Why PasteBox",
    workspaceBody: "Faster handoffs, links you control, files you can trust.",
    features: [
      {
        title: "Instant, no install",
        body: "Nothing to download. Open the browser and share. Guests can run the whole flow before signing up.",
        stat: "0 install",
      },
      {
        title: "Links you control",
        body: "Every link takes a password, visit and download caps, and an expiry. Sent it to the wrong person? Revoke it in one click.",
        stat: "revocable",
      },
      {
        title: "Files you can trust",
        body: "Attachments keep their virus-scan status, so the risk is clear before anyone downloads.",
        stat: "scanned",
      },
    ],
    steps: [
      {
        title: "Choose",
        body: "File mode is the default: pick or drop the file you want to send.",
      },
      {
        title: "Link",
        body: "Once every upload finishes, the share link and its expiry appear.",
      },
      {
        title: "Pick up",
        body: "The recipient opens the link, checks the list, and downloads each file.",
      },
    ],
    ctaTitle: "Send your first file right now.",
    ctaBody:
      "Register free and get history, bigger limits, and full link control instantly.",
    ctaBadges: ["Free to start", "No install", "Revocable", "Scanned uploads"],
  };
}

const orderStatusText: Record<
  string,
  Record<string, Omit<OrderStatusDetail, "tone">>
> = {
  en: {
    pending: {
      label: "Pending",
      description: "Waiting for provider confirmation.",
    },
    paid: { label: "Paid", description: "Membership is active." },
    failed: {
      label: "Failed",
      description: "Provider reported a payment failure.",
    },
    expired: {
      label: "Expired",
      description: "The payment window expired before confirmation.",
    },
    canceled: {
      label: "Canceled",
      description: "The provider order or subscription was canceled.",
    },
    refunded: {
      label: "Refunded",
      description: "Payment was refunded and matching access was revoked.",
    },
    needs_review: {
      label: "Needs review",
      description: "Support review is required before activation.",
    },
  },
  "zh-CN": {
    pending: { label: "待支付", description: "等待支付渠道确认。" },
    paid: { label: "已支付", description: "会员权益已生效。" },
    failed: { label: "支付失败", description: "支付渠道返回失败状态。" },
    expired: { label: "已过期", description: "支付窗口已过期，未确认到账。" },
    canceled: { label: "已取消", description: "渠道订单或订阅已取消。" },
    refunded: {
      label: "已退款",
      description: "已退款，匹配的会员权益已撤销。",
    },
    needs_review: { label: "需审核", description: "需要客服审核后再处理。" },
  },
  "zh-TW": {
    pending: { label: "待付款", description: "等待付款渠道確認。" },
    paid: { label: "已付款", description: "會員權益已生效。" },
    failed: { label: "付款失敗", description: "付款渠道返回失敗狀態。" },
    expired: { label: "已過期", description: "付款視窗已過期，未確認到帳。" },
    canceled: { label: "已取消", description: "渠道訂單或訂閱已取消。" },
    refunded: {
      label: "已退款",
      description: "已退款，匹配的會員權益已撤銷。",
    },
    needs_review: { label: "需審核", description: "需要客服審核後再處理。" },
  },
  es: {
    pending: {
      label: "Pendiente",
      description: "Esperando confirmación del proveedor.",
    },
    paid: { label: "Pagado", description: "La membresía está activa." },
    failed: {
      label: "Fallido",
      description: "El proveedor reportó un fallo de pago.",
    },
    expired: {
      label: "Vencido",
      description: "La ventana de pago venció antes de confirmarse.",
    },
    canceled: {
      label: "Cancelado",
      description: "El pedido o la suscripción fue cancelado.",
    },
    refunded: {
      label: "Reembolsado",
      description: "El pago fue reembolsado y el acceso relacionado revocado.",
    },
    needs_review: {
      label: "Requiere revisión",
      description: "Soporte debe revisar antes de activar.",
    },
  },
};

const attachmentScanText: Record<
  string,
  Record<string, Omit<AttachmentScanDetail, "tone" | "canDownload">>
> = {
  en: {
    clean: {
      label: "Clean",
      description: "Scan passed. Downloads are allowed.",
    },
    pending: {
      label: "Scan pending",
      description:
        "Owner download is allowed, but public share downloads wait for a clean scan.",
    },
    scan_failed: {
      label: "Scan failed",
      description:
        "Owner download is allowed with caution. Public share downloads are blocked until retry passes.",
    },
    malicious: {
      label: "Blocked",
      description:
        "Known malicious files are blocked for owner and public downloads.",
    },
  },
  "zh-CN": {
    clean: {
      label: "扫描通过",
      description: "文件已通过扫描，可以下载。",
    },
    pending: {
      label: "等待扫描",
      description: "所有者可下载，但公开分享下载需等待扫描通过。",
    },
    scan_failed: {
      label: "扫描失败",
      description: "所有者可谨慎下载；公开分享下载会阻止到重试通过为止。",
    },
    malicious: {
      label: "已阻止",
      description: "已知恶意文件会阻止所有者和公开下载。",
    },
  },
  "zh-TW": {
    clean: {
      label: "掃描通過",
      description: "檔案已通過掃描，可以下載。",
    },
    pending: {
      label: "等待掃描",
      description: "擁有者可下載，但公開分享下載需等待掃描通過。",
    },
    scan_failed: {
      label: "掃描失敗",
      description: "擁有者可謹慎下載；公開分享下載會阻止到重試通過為止。",
    },
    malicious: {
      label: "已阻止",
      description: "已知惡意檔案會阻止擁有者和公開下載。",
    },
  },
  es: {
    clean: {
      label: "Limpio",
      description: "Escaneo aprobado. Las descargas están permitidas.",
    },
    pending: {
      label: "Escaneo pendiente",
      description:
        "El propietario puede descargar, pero los enlaces públicos esperan un escaneo limpio.",
    },
    scan_failed: {
      label: "Escaneo fallido",
      description:
        "El propietario puede descargar con cautela. Los enlaces públicos quedan bloqueados hasta que un reintento pase.",
    },
    malicious: {
      label: "Bloqueado",
      description:
        "Los archivos maliciosos conocidos se bloquean para descargas privadas y públicas.",
    },
  },
};

function orderStatusTone(status: string): OrderStatusTone {
  switch (status.toLowerCase()) {
    case "paid":
      return "success";
    case "failed":
    case "canceled":
    case "refunded":
      return "danger";
    case "expired":
    case "needs_review":
      return "warning";
    case "pending":
      return "pending";
    default:
      return "neutral";
  }
}

function orderStatusDetail(status: string, locale: Locale): OrderStatusDetail {
  const normalized = status.toLowerCase();
  const detail = orderStatusText[locale][normalized] ?? {
    label: status || "Unknown",
    description:
      locale === "es"
        ? "El proveedor devolvió este estado."
        : isChineseLocale(locale)
          ? "支付渠道返回的状态。"
          : "Provider returned this status.",
  };
  return { ...detail, tone: orderStatusTone(normalized) };
}

function attachmentScanDetail(
  attachment: Attachment,
  locale: Locale,
  context: "owner" | "public",
): AttachmentScanDetail {
  const normalized = attachment.scanStatus.toLowerCase();
  const base = attachmentScanText[locale][normalized] ?? {
    label: attachment.scanStatus || "Unknown",
    description:
      locale === "es"
        ? "El escáner devolvió este estado."
        : isChineseLocale(locale)
          ? "扫描服务返回的状态。"
          : "Scanner returned this status.",
  };
  const canDownload =
    normalized === "malicious"
      ? false
      : context === "owner" || normalized === "clean";
  let tone: AttachmentScanTone = "neutral";
  if (normalized === "clean") tone = "success";
  if (normalized === "pending" || normalized === "scan_failed") {
    tone = "warning";
  }
  if (normalized === "malicious") tone = "danger";
  const risk = attachment.risk?.trim();
  const riskPrefix =
    locale === "es"
      ? "Riesgo"
      : locale === "zh-TW"
        ? "風險"
        : isChineseLocale(locale)
          ? "风险"
          : "Risk";
  return {
    ...base,
    description: risk
      ? `${base.description} ${riskPrefix}: ${risk}`
      : base.description,
    tone,
    canDownload,
  };
}

function paymentProviderOptions(price: Price): Array<{
  provider: PaymentProvider;
  label: string;
}> {
  const options: Array<{ provider: PaymentProvider; label: string }> = [];
  if (price.stripeEnabled) {
    options.push({ provider: "stripe", label: "Stripe" });
  }
  if (price.epusdtEnabled) {
    options.push({ provider: "epusdt", label: "Epusdt" });
  }
  return options;
}

function App() {
  const requestLocale = useMemo(
    () => localeFromRequestParams() ?? browserLocale,
    [],
  );
  const [user, setUser] = useState<User | null>(null);
  const [sessionChecked, setSessionChecked] = useState(false);
  const [catalog, setCatalog] = useState<PlanCatalog | null>(null);
  const [quota, setQuota] = useState<Quota | null>(null);
  const [pastes, setPastes] = useState<Paste[]>([]);
  const [shares, setShares] = useState<Share[]>([]);
  const [orders, setOrders] = useState<Order[]>([]);
  const [auditLogs, setAuditLogs] = useState<AuditLog[]>([]);
  const [adminStats, setAdminStats] = useState<Record<string, unknown> | null>(
    null,
  );
  const [adminData, setAdminData] = useState<AdminData>(emptyAdminData);
  const [adminPaymentReasons, setAdminPaymentReasons] = useState<
    Record<string, string>
  >({});
  const [redemptionDraft, setRedemptionDraft] = useState<RedemptionDraft>(
    defaultRedemptionDraft,
  );
  const [adminTab, setAdminTab] = useState<AdminTab>("overview");
  const [catalogSection, setCatalogSection] = useState<
    "plans" | "prices" | "redemptions"
  >("plans");
  const [catalogPlanId, setCatalogPlanId] = useState("free");
  const [catalogPriceId, setCatalogPriceId] = useState("");
  const [adminSizeUnits, setAdminSizeUnits] = useState<
    Record<string, SizeUnit>
  >({});
  const [adminTimeUnits, setAdminTimeUnits] = useState<
    Record<string, TimeUnit>
  >({});
  const [managedSecretDraft, setManagedSecretDraft] =
    useState<ManagedSecretPatch>({});
  const [alertTestMessage, setAlertTestMessage] = useState(
    "PasteBox alert test",
  );
  const [view, setView] = useState<View>("inbox");
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("all");
  const [tagFilter, setTagFilter] = useState("");
  const [draft, setDraft] = useState<Draft>(defaultDraft);
  const [selectedPasteId, setSelectedPasteId] = useState<string>("");
  const [editDraft, setEditDraft] = useState({
    id: "",
    title: "",
    text: "",
    tags: "",
  });
  const [shareDraft, setShareDraft] = useState<ShareDraft>(defaultShareDraft);
  // The send settings are the privacy knobs of one file send: they are read when
  // the transfer is created, so they apply to the whole batch.
  const [sendSettings, setSendSettings] =
    useState<SendSettings>(defaultSendSettings);
  const [shareToken, setShareToken] = useState("");
  const [publicShareToken, setPublicShareToken] = useState(shareTokenFromPath);
  const [publicSharePassword, setPublicSharePassword] = useState("");
  const [shareAccess, setShareAccess] = useState<{
    paste: Paste;
    share: Share;
  } | null>(null);
  const shareStatusRefresh = useRef({ token: "", attempts: 0 });
  const [authLink, setAuthLink] = useState<AuthLink | null>(() =>
    authLinkFromLocation(),
  );
  const [passwordResetLinkActive, setPasswordResetLinkActive] = useState(false);
  const [verificationToken, setVerificationToken] = useState("");
  const [reportDraft, setReportDraft] = useState({
    target: "",
    reason: "",
  });
  const [auth, setAuth] = useState({
    email: "",
    password: "",
    displayName: "",
    emailVerificationCode: "",
    turnstileToken: "",
  });
  const attachmentInputId = useId();
  const attachInputId = useId();
  const imageInputId = useId();
  const [resetToken, setResetToken] = useState("");
  const [profileDraft, setProfileDraft] = useState({
    displayName: "",
    language: requestLocale,
  });
  const [message, setMessage] = useState("");
  const [saveResults, setSaveResults] = useState<Record<string, SaveResult>>(
    {},
  );
  const [busy, setBusy] = useState(false);
  const [supportContacts, setSupportContacts] =
    useState<SupportContacts | null>(null);
  const locale = useMemo(
    () => localeFor(user?.language ?? requestLocale),
    [requestLocale, user?.language],
  );
  const t = useMemo(() => copyFor(locale), [locale]);
  const transferLabels = useMemo(() => transferCopyFor(locale), [locale]);
  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);
  // A published file stays unavailable to recipients until the virus scan
  // finishes, so re-read the share while anything is still pending. Status
  // refreshes consume a visit, so only unlimited-visit shares (the transfer
  // default) are refreshed here; a non-consuming status channel is tracked
  // separately.
  useEffect(() => {
    if (!shareAccess || shareAccess.share.maxVisits > 0) return;
    const share = shareAccess.share;
    const pending = shareAccess.paste.attachments.some(
      (attachment) => attachment.scanStatus === "pending",
    );
    if (!pending) return;
    if (shareStatusRefresh.current.token !== share.token) {
      shareStatusRefresh.current = { token: share.token, attempts: 0 };
    }
    if (shareStatusRefresh.current.attempts >= 20) return;
    shareStatusRefresh.current.attempts += 1;
    const timer = window.setTimeout(() => {
      void client
        .accessShare(share.token, publicSharePassword)
        .then((result) => setShareAccess(result))
        .catch(() => undefined);
    }, 3000);
    return () => window.clearTimeout(timer);
  }, [shareAccess, publicSharePassword]);
  const publicPage = useMemo(
    () =>
      typeof window !== "undefined"
        ? publicPageForPath(window.location.pathname)
        : null,
    [],
  );
  const currentPath = normalizedPathname();
  const publicShareRoute = Boolean(publicShareToken);
  const authRoute = authModeForPath(currentPath);
  const workspaceRoute = isWorkspacePath(currentPath);
  const shouldProbeSession = Boolean(
    authLink?.kind === "email-verification" ||
      workspaceRoute ||
      authRoute ||
      currentPath === "/",
  );

  useEffect(() => {
    if (
      user &&
      authRoute &&
      !passwordResetLinkActive &&
      authLink?.kind !== "password-reset"
    ) {
      moveToWorkspacePath();
      void refreshAuthed();
    }
  }, [user, authRoute, passwordResetLinkActive, authLink]);

  const activePlan = useMemo(() => {
    const planId = user?.planId ?? "free";
    return (
      quota?.plan ??
      catalog?.plans.find((plan) => plan.id === planId) ??
      catalog?.plans[0]
    );
  }, [catalog, quota, user]);
  const linkedOAuthProviders = user?.oauthProviders ?? [];
  const storageUsed = quota?.activeStorageBytes ?? 0;
  const storageLimit = activePlan?.activeStorageBytes ?? 0;
  const storagePercent =
    storageLimit > 0
      ? Math.min(100, Math.round((storageUsed / storageLimit) * 100))
      : 0;

  const selectedPaste = useMemo(
    () => pastes.find((paste) => paste.id === selectedPasteId) ?? pastes[0],
    [pastes, selectedPasteId],
  );
  const tagsPerPasteLimit = activePlan?.tagsPerPasteLimit ?? 0;
  const canCreateTags = tagsPerPasteLimit > 0;
  const draftTagCount = parseTagInput(draft.tags).length;
  const selectedTagsReadOnly = Boolean(
    selectedPaste && selectedPaste.tags.length > tagsPerPasteLimit,
  );
  const canEditSelectedTags = tagsPerPasteLimit > 0 && !selectedTagsReadOnly;

  const expiringCount = useMemo(
    () => pastes.filter((paste) => paste.secondsToLive <= 24 * 60 * 60).length,
    [pastes],
  );
  const attachmentCount = useMemo(
    () => pastes.reduce((total, paste) => total + paste.attachments.length, 0),
    [pastes],
  );
  const sharedPasteCount = useMemo(
    () => pastes.filter((paste) => paste.shareCount > 0).length,
    [pastes],
  );
  const workspaceStats: WorkspaceStat[] = useMemo(
    () => [
      {
        label: t("activePastesLabel"),
        value: String(quota?.activePasteCount ?? pastes.length),
        detail: `${activePlan?.activePasteLimit ?? 0} ${t("planLimit")}`,
        tone: "pastes",
      },
      {
        label: t("storage"),
        value: formatBytes(storageUsed),
        detail: `${storagePercent}% ${t("storageOf")} ${formatBytes(storageLimit)}`,
        tone: "storage",
      },
      {
        label: t("sharedLinks"),
        value: String(shares.length),
        detail: `${sharedPasteCount} ${t("pastesExposed")}`,
        tone: "shares",
      },
      {
        label: t("attachmentsLabel"),
        value: String(attachmentCount),
        detail: expiringCount
          ? `${expiringCount} ${t("expiringIn24h")}`
          : t("noUrgentExpiry"),
        tone: "attachments",
      },
    ],
    [
      activePlan?.activePasteLimit,
      attachmentCount,
      expiringCount,
      pastes.length,
      quota?.activePasteCount,
      sharedPasteCount,
      shares.length,
      storageLimit,
      storagePercent,
      storageUsed,
      t,
    ],
  );
  const viewSummary = viewSummaries[locale][view];

  const pricesByPlan = useMemo(() => {
    const grouped = new Map<string, Price[]>();
    for (const price of catalog?.prices ?? []) {
      const current = grouped.get(price.planId) ?? [];
      current.push(price);
      grouped.set(price.planId, current);
    }
    return grouped;
  }, [catalog]);

  const loadSupportContacts = useCallback(async () => {
    try {
      setSupportContacts(await client.supportContacts());
    } catch {
      setSupportContacts(null);
    }
  }, []);

  const loadCore = useCallback(async () => {
    const [planCatalog, meResult] = await Promise.allSettled([
      client.plans(),
      client.me(),
    ]);
    if (planCatalog.status === "fulfilled") {
      setCatalog(planCatalog.value);
    }
    if (meResult.status === "fulfilled") {
      setUser(meResult.value);
      setProfileDraft({
        displayName: meResult.value.displayName,
        language: localeFor(meResult.value.language),
      });
      if (isWorkspacePath(normalizedPathname())) await refreshAuthed();
    }
    setSessionChecked(true);
  }, []);

  const refreshAuthed = useCallback(async () => {
    const [quotaResult, pasteResult, shareResult, orderResult] =
      await Promise.allSettled([
        client.quota(),
        client.pastes(searchParams(query, filter, tagFilter)),
        client.shares(),
        client.orders(),
      ]);
    if (quotaResult.status === "fulfilled") setQuota(quotaResult.value);
    if (pasteResult.status === "fulfilled") {
      setPastes(pasteResult.value.pastes);
      if (!selectedPasteId && pasteResult.value.pastes[0]) {
        setSelectedPasteId(pasteResult.value.pastes[0].id);
      }
    }
    if (shareResult.status === "fulfilled") setShares(shareResult.value.shares);
    if (orderResult.status === "fulfilled") setOrders(orderResult.value.orders);
  }, [filter, query, selectedPasteId, tagFilter]);

  // The lifetime a send may promise comes from the active plan, so the composer
  // cannot offer a duration the service would shorten.
  const sendRetentionSeconds = activePlan?.maxRetentionSeconds ?? 0;
  const sendExpiryOptions = useMemo(
    () => expiryOptionsFor(sendRetentionSeconds, draft.expiresInSeconds, t),
    [sendRetentionSeconds, draft.expiresInSeconds, t],
  );
  // A plan change must not leave the composer promising a lifetime the plan no
  // longer allows.
  useEffect(() => {
    if (sendRetentionSeconds <= 0) return;
    setDraft((current) => {
      const clamped = clampExpirySeconds(
        current.expiresInSeconds,
        sendRetentionSeconds,
      );
      return clamped === current.expiresInSeconds
        ? current
        : { ...current, expiresInSeconds: clamped };
    });
  }, [sendRetentionSeconds]);

  const [sendMode, setSendMode] = useState<SendMode>("file");
  // Tags name the record a send creates, so the composer's tag input applies to
  // every mode and stays plan-gated like any other record field.
  const sendTags = useMemo(
    () => (canCreateTags ? parseTagInput(draft.tags) : []),
    [canCreateTags, draft.tags],
  );
  // One send is a queue of files that share a single transfer, so the account
  // entry point and the guest workbench behave the same way. File and image mode
  // send the same shape (a manifest), which is why they share one adapter.
  const sendAdapter = useMemo<TransferQueueAdapter>(
    () => ({
      create: async (manifest, idempotencyKey) => {
        const created = await client.createTransfer({
          idempotencyKey,
          expiresInSeconds: draft.expiresInSeconds,
          password: sendSettings.password,
          loginRequired: sendSettings.loginRequired,
          title: draft.title.trim() || undefined,
          tags: sendTags,
          items: manifest,
        });
        return created.transfer.id;
      },
      upload: async (transferId, itemId, file, onProgress, signal) => {
        await client.uploadTransferItem(
          transferId,
          itemId,
          file,
          onProgress,
          signal,
        );
      },
      publish: async (transferId) => {
        const published = await client.publishTransfer(transferId);
        return shareFromTransfer(published.transfer);
      },
      cancel: async (transferId) => {
        await client.cancelTransfer(transferId);
      },
    }),
    [
      draft.expiresInSeconds,
      draft.title,
      sendSettings.loginRequired,
      sendSettings.password,
      sendTags,
    ],
  );
  // Text mode has no files to upload: its transfer is created with the body and
  // published straight away.
  const textSendAdapter = useMemo<TextSendAdapter>(
    () => ({
      create: async (idempotencyKey) => {
        const created = await client.createTransfer({
          idempotencyKey,
          expiresInSeconds: draft.expiresInSeconds,
          password: sendSettings.password,
          loginRequired: sendSettings.loginRequired,
          title: draft.title.trim() || undefined,
          text: draft.text,
          tags: sendTags,
          items: [],
        });
        return created.transfer.id;
      },
      publish: async (transferId) => {
        const published = await client.publishTransfer(transferId);
        return shareFromTransfer(published.transfer);
      },
    }),
    [
      draft.expiresInSeconds,
      draft.text,
      draft.title,
      sendSettings.loginRequired,
      sendSettings.password,
      sendTags,
    ],
  );
  const sendQueue = useTransferQueue(sendAdapter);
  const imageQueue = useTransferQueue(sendAdapter);
  const textSend = useTextSend(textSendAdapter);
  const activeSendQueue = sendMode === "image" ? imageQueue : sendQueue;
  const sendLimits = useMemo(
    () => ({
      singleFileBytes: activePlan?.singleFileBytes ?? 0,
      singlePasteBytes: activePlan?.singlePasteBytes ?? 0,
      attachmentsPerPasteLimit: activePlan?.attachmentsPerPasteLimit ?? 0,
    }),
    [activePlan],
  );
  const sendShare =
    sendMode === "text"
      ? textSend.phase === "sent"
        ? textSend.share
        : null
      : activeSendQueue.phase === "published"
        ? activeSendQueue.share
        : null;
  // A finished send is a new record, so surface it in the workspace and refresh
  // the quota and record list the send just changed.
  useEffect(() => {
    if (!sendShare?.pasteId) return;
    setSelectedPasteId(sendShare.pasteId);
    void refreshAuthed();
  }, [refreshAuthed, sendShare]);

  const refreshAdmin = useCallback(async () => {
    if (user?.role !== "admin") return;
    const [
      stats,
      users,
      pastesResult,
      attachments,
      sharesResult,
      ordersResult,
      queues,
      runtimeConfig,
      managedConfig,
      runtimePanel,
      manualWorkItems,
      redemptionBatches,
      alerts,
      webhooks,
      logs,
    ] = await Promise.allSettled([
      client.adminDashboard(),
      client.adminUsers(),
      client.adminPastes(),
      client.adminAttachments(""),
      client.adminShares(),
      client.adminOrders(),
      client.adminQueues(),
      client.adminRuntimeConfig(),
      client.adminManagedConfig(),
      client.adminRuntimePanel(),
      client.adminManualWorkItems(),
      client.adminRedemptionBatches(),
      client.adminAlerts(),
      client.adminWebhookEvents(),
      client.adminAuditLogs(),
    ]);
    if (stats.status === "fulfilled") setAdminStats(stats.value);
    setAdminData({
      users: users.status === "fulfilled" ? users.value.users : [],
      pastes:
        pastesResult.status === "fulfilled" ? pastesResult.value.pastes : [],
      attachments:
        attachments.status === "fulfilled" ? attachments.value.attachments : [],
      shares:
        sharesResult.status === "fulfilled" ? sharesResult.value.shares : [],
      orders:
        ordersResult.status === "fulfilled" ? ordersResult.value.orders : [],
      queues: queues.status === "fulfilled" ? queues.value : null,
      runtimeConfig:
        runtimeConfig.status === "fulfilled" ? runtimeConfig.value : null,
      managedConfig:
        managedConfig.status === "fulfilled" ? managedConfig.value : null,
      runtimePanel:
        runtimePanel.status === "fulfilled" ? runtimePanel.value : null,
      manualWorkItems:
        manualWorkItems.status === "fulfilled"
          ? manualWorkItems.value.items
          : [],
      redemptionBatches:
        redemptionBatches.status === "fulfilled"
          ? redemptionBatches.value.batches
          : [],
      alerts: alerts.status === "fulfilled" ? alerts.value.alerts : [],
      webhookEvents:
        webhooks.status === "fulfilled" ? webhooks.value.webhookEvents : [],
    });
    if (logs.status === "fulfilled") setAuditLogs(logs.value.auditLogs);
  }, [user?.role]);

  useEffect(() => {
    if (publicPage || catalog) return;
    void client
      .plans()
      .then(setCatalog)
      .catch(() => undefined);
  }, [catalog, publicPage]);

  useEffect(() => {
    if (!publicPage) return;
    void loadSupportContacts();
  }, [loadSupportContacts, publicPage]);

  useEffect(() => {
    if (publicPage) return;
    if (!shouldProbeSession) return;
    void loadCore();
  }, [loadCore, publicPage, shouldProbeSession]);

  useEffect(() => {
    if (publicPage) return;
    if (user && workspaceRoute) void refreshAuthed();
  }, [filter, publicPage, query, user]);

  useEffect(() => {
    if (!authLink || publicPage) return;

    const link = authLink;
    setAuthLink(null);
    clearAuthLinkTokenFromLocation();

    if (link.kind === "password-reset") {
      setResetToken(link.token);
      setPasswordResetLinkActive(true);
      setMessage("");
      return;
    }

    async function completeAuthLink() {
      const updated = await run(
        () => client.finishEmailVerification(link.token),
        t("emailVerified"),
      );
      if (updated) {
        await applyVerifiedEmail(updated);
      }
    }

    void completeAuthLink();
  }, [authLink, loadCore, publicPage, refreshAuthed, t, user]);

  useEffect(() => {
    if (!selectedPaste) {
      setEditDraft({ id: "", title: "", text: "", tags: "" });
      return;
    }
    setEditDraft({
      id: selectedPaste.id,
      title: selectedPaste.title,
      text: selectedPaste.text,
      tags: selectedPaste.tags.join(", "),
    });
  }, [selectedPaste?.id]);

  async function run<T>(
    action: () => Promise<T>,
    success: string,
    saveKey?: string,
  ): Promise<T | null> {
    setBusy(true);
    setMessage("");
    if (saveKey)
      setSaveResults((previous) => ({
        ...previous,
        [saveKey]: { text: t("saving"), error: false },
      }));
    try {
      const result = await action();
      if (!saveKey) setMessage(success);
      if (saveKey)
        setSaveResults((previous) => ({
          ...previous,
          [saveKey]: { text: success, error: false },
        }));
      return result;
    } catch (error) {
      const apiError = error as ApiError;
      if (!saveKey) setMessage(apiError.message || t("requestFailed"));
      if (saveKey)
        setSaveResults((previous) => ({
          ...previous,
          [saveKey]: {
            text: apiError.message || t("requestFailed"),
            error: true,
          },
        }));
      return null;
    } finally {
      setBusy(false);
    }
  }

  async function applyVerifiedEmail(updated: User) {
    setAuth((current) => ({ ...current, email: updated.email }));
    setVerificationToken("");
    if (!user) {
      setMessage(t("emailVerifiedLogin"));
      await loadCore();
      return;
    }
    if (updated.id !== user.id) {
      setMessage(t("emailVerifiedDifferentAccount"));
      return;
    }
    setUser(updated);
    setProfileDraft({
      displayName: updated.displayName,
      language: localeFor(updated.language),
    });
    await refreshAuthed();
  }

  async function register() {
    if (user || !sessionChecked) return;
    const result = await run(
      () =>
        client.register({
          email: auth.email,
          password: auth.password,
          displayName: auth.displayName,
          language: locale,
          emailVerificationCode: auth.emailVerificationCode,
          turnstileToken: auth.turnstileToken,
        }),
      t("accountReady"),
    );
    if (result) {
      setUser(result.user);
      setVerificationToken(result.devEmailVerificationToken ?? "");
      setProfileDraft({
        displayName: result.user.displayName,
        language: localeFor(result.user.language),
      });
      moveToWorkspacePath();
      await refreshAuthed();
    }
  }

  async function login() {
    const result = await run(
      () => client.login({ email: auth.email, password: auth.password }),
      t("signedIn"),
    );
    if (result) {
      setUser(result.user);
      setVerificationToken("");
      setProfileDraft({
        displayName: result.user.displayName,
        language: localeFor(result.user.language),
      });
      moveToWorkspacePath();
      await refreshAuthed();
    }
  }

  async function startRegistrationVerification() {
    const result = await run(
      () => client.startRegistrationVerification(auth.email),
      t("registrationCodeIssued"),
    );
    if (result?.devToken) {
      setAuth((current) => ({
        ...current,
        emailVerificationCode: result.devToken ?? "",
      }));
    }
  }

  function googleOAuth() {
    window.location.assign(client.googleOAuthStartPath("/app", locale));
  }

  function githubOAuth() {
    window.location.assign(client.githubOAuthStartPath("/app", locale));
  }

  async function startVerification() {
    const result = await run(
      () => client.startEmailVerification(),
      t("verificationIssued"),
    );
    if (result?.devToken) setVerificationToken(result.devToken);
  }

  async function finishVerification() {
    const updated = await run(
      () => client.finishEmailVerification(verificationToken),
      t("emailVerified"),
    );
    if (updated) {
      await applyVerifiedEmail(updated);
    }
  }

  async function logout() {
    await run(() => client.logout(), t("signedOut"));
    setUser(null);
    setPastes([]);
    setShares([]);
    setQuota(null);
  }

  async function logoutAll() {
    await run(() => client.logoutAll(), t("allSessionsSignedOut"));
    setUser(null);
    setPastes([]);
    setShares([]);
    setQuota(null);
  }

  async function passwordReset() {
    const result = await run(
      () => client.passwordReset(auth.email),
      t("passwordResetIssued"),
    );
    if (result?.devToken) {
      setResetToken(result.devToken);
      setPasswordResetLinkActive(true);
      setMessage("");
    }
  }

  async function finishPasswordReset() {
    const result = await run(
      () => client.finishPasswordReset(resetToken, auth.password),
      t("passwordUpdated"),
    );
    if (result) {
      setResetToken("");
      setPasswordResetLinkActive(false);
      setAuth((current) => ({ ...current, password: "" }));
      if (
        typeof window !== "undefined" &&
        normalizedPathname() === "/password-reset"
      ) {
        window.history.replaceState(null, "", "/login");
      }
    }
  }

  function cancelPasswordReset() {
    setResetToken("");
    setPasswordResetLinkActive(false);
    setMessage("");
    if (
      typeof window !== "undefined" &&
      normalizedPathname() === "/password-reset"
    ) {
      window.history.replaceState(null, "", "/login");
    }
  }

  async function submitReport(target = reportDraft.target) {
    const report = await run(
      () => client.report({ target, reason: reportDraft.reason || "abuse" }),
      t("reportSubmitted"),
    );
    if (report) {
      setReportDraft({ target: "", reason: "" });
      await refreshAdmin();
    }
  }

  async function createPaste() {
    const paste = await run(
      () =>
        client.createPaste({
          title: draft.title,
          text: draft.text,
          tags: draft.tags
            ? canCreateTags
              ? parseTagInput(draft.tags)
              : []
            : [],
          pinned: draft.pinned,
          favorite: draft.favorite,
          expiresInSeconds: draft.expiresInSeconds,
        }),
      t("pasteCreated"),
    );
    if (paste) {
      setDraft(defaultDraft);
      setSelectedPasteId(paste.id);
      await refreshAuthed();
    }
  }

  // The send area declares every file before anything is uploaded, because the
  // server publishes one share for the whole manifest only once all of it has
  // landed. Files can only join a send that has not started yet.
  // Switching modes keeps every draft: staged files, a staged image and the
  // typed text all stay where they were, so a switch cannot lose work.
  function switchSendMode(nextMode: SendMode) {
    setSendMode(nextMode);
    setMessage("");
  }

  function stageSendFiles(files: File[]) {
    if (files.length === 0) return;
    const labels = transferCopyFor(locale);
    const violation = checkSendLimits(sendQueue.items, files, sendLimits);
    if (violation) {
      setMessage(sendLimitCopy(violation, labels));
      return;
    }
    if (!sendQueue.addFiles(files)) {
      setMessage(labels.manifestFrozen);
      return;
    }
    setMessage("");
  }

  // Image mode sends one image; the shared helper decides whether a pick
  // replaces a draft, starts over, or has to be refused.
  async function stageSendImage(files: File[]) {
    const result = await stageOneImage(
      imageQueue,
      files[0],
      sendLimits,
      transferCopyFor(locale),
    );
    if (result.kind === "error" || result.kind === "limit") {
      setMessage(result.message);
    } else if (result.kind === "staged") {
      setMessage("");
    }
  }

  // A text send is one create plus one publish; the body is the content.
  function sendTextDraft() {
    if (draft.text.trim() === "") return;
    setMessage("");
    void textSend.send(`${draft.title}\u0000${draft.text}`);
  }

  const activeSendStatusText = sendQueueStatus(
    activeSendQueue.phase,
    activeSendQueue.counts,
    activeSendQueue.error,
    transferLabels,
  );
  const textSendStatusText = textSendStatus(
    textSend.phase,
    textSend.error,
    transferLabels,
  );

  // A cancel only claims success when the server confirmed it dropped the draft.
  async function cancelSend(queue: TransferQueue) {
    const labels = transferCopyFor(locale);
    const canceled = await queue.cancel();
    setMessage(canceled ? labels.canceled : labels.failed);
  }

  // Attaching a file to an existing record stays an explicit action, so the
  // file-first entry point cannot silently modify a historical note.
  async function attachFile(file: File) {
    let targetPaste = selectedPaste;
    if (!targetPaste) {
      const createdPaste = await run(
        () =>
          client.createPaste({
            title: file.name,
            text: "",
            tags: [],
            pinned: false,
            favorite: false,
            expiresInSeconds: draft.expiresInSeconds,
          }),
        t("pasteCreated"),
      );
      if (!createdPaste) return;
      targetPaste = createdPaste;
      setSelectedPasteId(targetPaste.id);
    }

    const uploaded = await run(
      () => client.uploadAttachment(targetPaste.id, file),
      t("attachmentUploaded"),
    );
    if (uploaded) await refreshAuthed();
  }

  async function createShare() {
    if (!selectedPaste) return;
    const share = await run(
      () =>
        client.createShare(selectedPaste.id, {
          password: shareDraft.password,
          loginRequired: shareDraft.loginRequired,
          maxVisits: shareDraft.maxVisits,
          maxDownloads: shareDraft.maxDownloads,
          expiresInSeconds: shareDraft.expiresInSeconds,
        }),
      t("shareLinkCreated"),
    );
    if (share) {
      setShareToken(share.token);
      await refreshAuthed();
    }
  }

  async function openShare(token = shareToken) {
    const result = await run(
      () => client.accessShare(token, shareDraft.password),
      t("shareOpened"),
    );
    if (result) setShareAccess(result);
  }

  async function openPublicShare() {
    const result = await run(
      () => client.accessShare(publicShareToken, publicSharePassword),
      t("shareOpened"),
    );
    if (result) setShareAccess(result);
  }

  async function saveSelectedPaste() {
    if (!selectedPaste || editDraft.id !== selectedPaste.id) return;
    const updated = await run(
      () =>
        client.updatePaste(selectedPaste.id, {
          title: editDraft.title,
          text: editDraft.text,
          tags: canEditSelectedTags
            ? parseTagInput(editDraft.tags)
            : selectedPaste.tags,
        }),
      t("pasteUpdated"),
    );
    if (updated) {
      setPastes((items) =>
        items.map((item) => (item.id === updated.id ? updated : item)),
      );
      await refreshAuthed();
    }
  }

  async function updatePasteFlag(paste: Paste, field: "pinned" | "favorite") {
    const updated = await run(
      () => client.updatePaste(paste.id, { [field]: !paste[field] }),
      field === "pinned" ? t("pinUpdated") : t("favoriteUpdated"),
    );
    if (updated) {
      setPastes((items) =>
        items.map((item) => (item.id === updated.id ? updated : item)),
      );
      await refreshAuthed();
    }
  }

  async function extendPaste(paste: Paste, expiresInSeconds: number) {
    const updated = await run(
      () => client.extendPaste(paste.id, expiresInSeconds),
      t("expirationExtended"),
    );
    if (updated) {
      setPastes((items) =>
        items.map((item) => (item.id === updated.id ? updated : item)),
      );
      await refreshAuthed();
    }
  }

  async function makeOrder(
    planId: string,
    period: string,
    provider: PaymentProvider,
  ) {
    const order = await run(
      () => client.createOrder({ provider, planId, period }),
      t("orderCreated"),
    );
    if (order) await refreshAuthed();
  }

  async function exportData() {
    const payload = await run(() => client.exportMe(), t("exportGenerated"));
    if (payload) {
      const url = URL.createObjectURL(
        new Blob([JSON.stringify(payload, null, 2)], {
          type: "application/json",
        }),
      );
      const link = document.createElement("a");
      link.href = url;
      link.download = "pastebox-export.json";
      link.click();
      URL.revokeObjectURL(url);
    }
  }

  async function requestDelete() {
    const updated = await run(
      () => client.requestDelete(),
      t("deletionScheduled"),
    );
    if (updated) setUser(updated);
  }

  async function cancelDelete() {
    const updated = await run(
      () => client.cancelDelete(),
      t("deletionCanceled"),
    );
    if (updated) setUser(updated);
  }

  async function executeDelete() {
    const result = await run(() => client.executeDelete(), t("accountDeleted"));
    if (result) {
      setUser(null);
      setPastes([]);
      setShares([]);
      setQuota(null);
    }
  }

  async function updateProfile() {
    const nextLocale = localeFor(profileDraft.language);
    const nextT = copyFor(nextLocale);
    const updated = await run(
      () => client.updateMe(profileDraft),
      nextT("profileUpdated"),
    );
    if (updated) {
      setUser(updated);
      setProfileDraft({
        displayName: updated.displayName,
        language: localeFor(updated.language),
      });
    }
  }

  async function unlinkOAuth(provider: string) {
    const updated = await run(
      () => client.unlinkOAuth(provider),
      t("oauthUnlinked"),
    );
    if (updated) setUser(updated);
  }

  async function runCleanup() {
    await run(() => client.runCleanup(), t("cleanupCompleted"));
    await refreshAuthed();
    await refreshAdmin();
  }

  async function runBillingReconciliation() {
    await run(() => client.adminReconcileBilling(), t("billingReconciled"));
    await refreshAuthed();
    await refreshAdmin();
  }

  async function adminRetryScan(attachmentId: string) {
    await run(() => client.adminRetryScan(attachmentId), t("scanRetried"));
    await refreshAdmin();
  }

  async function adminFreezeAttachment(attachmentId: string, frozen: boolean) {
    await run(
      () => client.adminFreezeAttachment(attachmentId, frozen),
      frozen ? t("attachmentFrozen") : t("attachmentReleased"),
    );
    await refreshAdmin();
  }

  async function adminRevokeShare(shareId: string) {
    await run(() => client.adminRevokeShare(shareId), t("shareRevoked"));
    await refreshAdmin();
    await refreshAuthed();
  }

  async function adminMarkOrderPaid(orderId: string) {
    const reason = (adminPaymentReasons[orderId] ?? "").trim();
    if (!reason) {
      setMessage(t("manualPaymentReasonRequired"));
      return;
    }
    const updatedOrder = await run(
      () => client.adminMarkOrderPaid(orderId, `manual-${Date.now()}`, reason),
      t("orderMarkedPaid"),
    );
    if (!updatedOrder) return;
    setAdminPaymentReasons((previous) => {
      const next = { ...previous };
      delete next[orderId];
      return next;
    });
    await refreshAdmin();
  }

  async function adminReplayWebhook(eventId: string) {
    await run(
      () => client.adminReplayWebhookEvent(eventId),
      t("webhookReplayed"),
    );
    await refreshAdmin();
  }

  async function adminResolveReport(
    report: Report,
    status: "resolved" | "dismissed",
  ) {
    await run(
      () => client.adminResolveReport(report.id, status),
      t("reportUpdated"),
    );
    await refreshAdmin();
  }

  function updateCatalogPlan(
    planId: string,
    patch: Partial<PlanCatalog["plans"][number]>,
  ) {
    setCatalog((previous) => {
      if (!previous) return previous;
      return {
        ...previous,
        plans: previous.plans.map((plan) =>
          plan.id === planId ? { ...plan, ...patch } : plan,
        ),
      };
    });
  }

  function updateCatalogPrice(priceId: string, patch: Partial<Price>) {
    setCatalog((previous) => {
      if (!previous) return previous;
      return {
        ...previous,
        prices: previous.prices.map((price) =>
          price.id === priceId ? { ...price, ...patch } : price,
        ),
      };
    });
  }

  function updateRuntimeGuestConfig(
    patch: Partial<RuntimeConfig["guestUploads"]>,
  ) {
    setAdminData((previous) => {
      if (!previous.runtimeConfig) return previous;
      return {
        ...previous,
        runtimeConfig: {
          ...previous.runtimeConfig,
          guestUploads: {
            ...previous.runtimeConfig.guestUploads,
            ...patch,
          },
        },
      };
    });
  }

  function updateRuntimeRegistrationConfig(
    patch: Partial<RuntimeConfig["registration"]>,
  ) {
    setAdminData((previous) => {
      if (!previous.runtimeConfig) return previous;
      return {
        ...previous,
        runtimeConfig: {
          ...previous.runtimeConfig,
          registration: {
            ...previous.runtimeConfig.registration,
            ...patch,
          },
        },
      };
    });
  }

  function updateRuntimeRateLimitConfig(
    patch: Partial<RuntimeConfig["rateLimits"]>,
  ) {
    setAdminData((previous) => {
      if (!previous.runtimeConfig) return previous;
      return {
        ...previous,
        runtimeConfig: {
          ...previous.runtimeConfig,
          rateLimits: {
            ...previous.runtimeConfig.rateLimits,
            ...patch,
          },
        },
      };
    });
  }

  function updateRuntimeAlertConfig(patch: Partial<RuntimeConfig["alerts"]>) {
    setAdminData((previous) => {
      if (!previous.runtimeConfig) return previous;
      return {
        ...previous,
        runtimeConfig: {
          ...previous.runtimeConfig,
          alerts: {
            ...previous.runtimeConfig.alerts,
            ...patch,
          },
        },
      };
    });
  }

  function updateRuntimeLogLevel(logLevel: LogLevel) {
    setAdminData((previous) => {
      if (!previous.runtimeConfig) return previous;
      return {
        ...previous,
        runtimeConfig: {
          ...previous.runtimeConfig,
          logLevel,
        },
      };
    });
  }

  function updateManagedConfig(config: ManagedConfig) {
    setAdminData((previous) =>
      previous.managedConfig
        ? {
            ...previous,
            managedConfig: { ...previous.managedConfig, config },
          }
        : previous,
    );
  }

  function updateManagedTurnstile(patch: Partial<ManagedConfig["turnstile"]>) {
    setAdminData((previous) =>
      previous.managedConfig
        ? {
            ...previous,
            managedConfig: {
              ...previous.managedConfig,
              config: {
                ...previous.managedConfig.config,
                turnstile: {
                  ...previous.managedConfig.config.turnstile,
                  ...patch,
                },
              },
            },
          }
        : previous,
    );
  }

  function updateManagedSecret(
    name: keyof ManagedSecretPatch,
    value: string | undefined,
  ) {
    setManagedSecretDraft((previous) => ({ ...previous, [name]: value }));
  }

  function adminSizeUnitFor(key: string, valueBytes: number): SizeUnit {
    return adminSizeUnits[key] ?? preferredSizeUnit(valueBytes);
  }

  function setAdminSizeUnit(key: string, unit: SizeUnit) {
    setAdminSizeUnits((previous) => ({ ...previous, [key]: unit }));
  }

  function adminTimeUnitFor(key: string, valueSeconds: number): TimeUnit {
    return adminTimeUnits[key] ?? preferredTimeUnit(valueSeconds);
  }

  function setAdminTimeUnit(key: string, unit: TimeUnit) {
    setAdminTimeUnits((previous) => ({ ...previous, [key]: unit }));
  }

  async function saveAdminCatalog(kind: "plans" | "prices", id: string) {
    if (!catalog) return;
    const body =
      kind === "plans"
        ? { plans: catalog.plans.filter((plan) => plan.id === id) }
        : {
            prices: catalog.prices
              .filter((price) => price.id === id)
              .map(
                ({
                  id,
                  planId,
                  period,
                  amountCents,
                  currency,
                  visible,
                  purchaseEnabled,
                }) => ({
                  id,
                  planId,
                  period,
                  amountCents,
                  currency,
                  visible,
                  purchaseEnabled,
                }),
              ),
          };
    const updated = await run(
      () => client.adminUpdateCatalogEntries(body),
      t("catalogSaved"),
      `${kind}-${id}`,
    );
    if (updated) {
      setCatalog((previous) =>
        previous
          ? {
              ...previous,
              plans: previous.plans.map((entry) =>
                kind === "plans" && entry.id === id
                  ? (updated.plans.find((item) => item.id === id) ?? entry)
                  : entry,
              ),
              prices: previous.prices.map((entry) =>
                kind === "prices" && entry.id === id
                  ? (updated.prices.find((item) => item.id === id) ?? entry)
                  : entry,
              ),
            }
          : updated,
      );
      await refreshAuthed();
    }
  }

  async function saveRuntimeConfig() {
    const cfg = adminData.runtimeConfig;
    if (!cfg) return;
    const managed = adminData.managedConfig;
    const security = adminTab === "security";
    const updated = await run(
      async () => {
        if (security && managed) {
          const saved = await client.adminUpdateManagedConfig({
            fields: ["turnstile"],
            config: { turnstile: managed.config.turnstile },
            secrets: {
              turnstileSecretKey: managedSecretDraft.turnstileSecretKey,
            },
          });
          setAdminData((previous) => ({
            ...previous,
            managedConfig: previous.managedConfig
              ? {
                  ...previous.managedConfig,
                  secrets: {
                    ...previous.managedConfig.secrets,
                    turnstileSecretKey: saved.secrets.turnstileSecretKey,
                  },
                }
              : saved,
          }));
        }
        return client.adminUpdateRuntimeConfig(
          security
            ? { registration: cfg.registration, rateLimits: cfg.rateLimits }
            : adminTab === "guest"
              ? { guestUploads: cfg.guestUploads }
              : { logLevel: cfg.logLevel, alerts: cfg.alerts },
        );
      },
      t("runtimeConfigSaved"),
      `runtime-${adminTab}`,
    );
    if (updated) {
      setAdminData((previous) => ({
        ...previous,
        runtimeConfig: previous.runtimeConfig
          ? {
              ...previous.runtimeConfig,
              ...(security
                ? {
                    registration: updated.registration,
                    rateLimits: updated.rateLimits,
                  }
                : adminTab === "guest"
                  ? { guestUploads: updated.guestUploads }
                  : { logLevel: updated.logLevel, alerts: updated.alerts }),
            }
          : updated,
        runtimePanel: previous.runtimePanel
          ? { ...previous.runtimePanel, config: updated }
          : null,
      }));
      if (security) {
        setManagedSecretDraft((previous) => ({
          ...previous,
          turnstileSecretKey: undefined,
        }));
      }
    }
  }

  async function saveManagedConfig(group: (typeof managedGroups)[number]) {
    const managed = adminData.managedConfig;
    if (!managed) return;
    const config = group.fields.reduce<Partial<ManagedConfig>>(
      (selected, key) => ({ ...selected, [key]: managed.config[key] }),
      {},
    );
    const secrets = group.secrets.reduce<ManagedSecretPatch>(
      (selected, key) => ({ ...selected, [key]: managedSecretDraft[key] }),
      {},
    );
    const updated = await run(
      () =>
        client.adminUpdateManagedConfig({
          config,
          fields: group.fields,
          secrets,
        }),
      t("managedConfigSaved"),
      `managed-${group.id}`,
    );
    if (updated) {
      setAdminData((previous) => ({
        ...previous,
        managedConfig: previous.managedConfig
          ? {
              ...previous.managedConfig,
              secrets: updated.secrets,
              config: group.fields.reduce(
                (current, key) => ({ ...current, [key]: updated.config[key] }),
                previous.managedConfig.config,
              ),
            }
          : updated,
      }));
      setManagedSecretDraft((previous) => {
        const next = { ...previous };
        for (const key of group.secrets) delete next[key];
        return next;
      });
    }
  }

  async function toggleGuestUploads() {
    const cfg = adminData.runtimeConfig?.guestUploads;
    if (!cfg) return;
    updateRuntimeGuestConfig({ enabled: !cfg.enabled });
  }

  async function toggleRuntimeAlerts() {
    const cfg = adminData.runtimeConfig?.alerts;
    if (!cfg) return;
    updateRuntimeAlertConfig({ enabled: !cfg.enabled });
  }

  async function adminProviderTest(provider: string) {
    await run(() => client.adminProviderTest(provider), t("providerTested"));
    await refreshAdmin();
  }

  async function createRedemptionBatch() {
    const created = await run(
      () =>
        client.adminCreateRedemptionBatch({
          planId: redemptionDraft.planId,
          durationDays: redemptionDraft.durationDays,
          quantity: redemptionDraft.quantity,
          note: redemptionDraft.note,
        }),
      t("redemptionBatchCreated"),
      "redemption-create",
    );
    if (created) {
      setRedemptionDraft((previous) => ({
        ...defaultRedemptionDraft,
        planId: previous.planId,
      }));
      await refreshAdmin();
    }
  }

  async function toggleRedemptionBatch(batch: RedemptionBatch) {
    await run(
      () =>
        client.adminUpdateRedemptionBatch(batch.id, {
          disabled: !batch.disabled,
          note: batch.note ?? "",
        }),
      t("redemptionBatchUpdated"),
      `redemption-${batch.id}`,
    );
    await refreshAdmin();
  }

  async function sendAdminTestAlert() {
    await run(
      () => client.adminSendTestAlert(alertTestMessage),
      t("alertSent"),
    );
    await refreshAdmin();
  }

  if (publicPage) {
    return (
      <PublicPageScreen
        page={publicPage}
        contacts={supportContacts}
        locale={locale}
      />
    );
  }

  if (publicShareToken) {
    return (
      <PublicShareScreen
        token={publicShareToken}
        password={publicSharePassword}
        access={shareAccess}
        message={message}
        busy={busy}
        onToken={setPublicShareToken}
        onPassword={setPublicSharePassword}
        onOpen={() => void openPublicShare()}
        locale={locale}
      />
    );
  }

  if (authRoute && !sessionChecked && !passwordResetLinkActive) {
    return (
      <main className="session-loading" role="status">
        {t("loadingSession")}
      </main>
    );
  }

  if (authRoute && (!user || passwordResetLinkActive)) {
    return (
      <AuthScreen
        mode={authRoute}
        auth={auth}
        busy={busy}
        message={message}
        passwordResetLinkActive={passwordResetLinkActive}
        onAuth={setAuth}
        onLogin={() => void login()}
        onRegister={() => void register()}
        onStartRegistrationVerification={() =>
          void startRegistrationVerification()
        }
        onGoogle={googleOAuth}
        onGithub={githubOAuth}
        onPasswordReset={() => void passwordReset()}
        onFinishPasswordReset={() => void finishPasswordReset()}
        onCancelPasswordReset={cancelPasswordReset}
        registration={catalog?.registration}
        locale={locale}
      />
    );
  }

  if (currentPath === "/" || !user) {
    return (
      <LandingPage
        catalog={catalog}
        locale={locale}
        user={user}
        sessionChecked={sessionChecked}
      />
    );
  }

  return (
    <main className="app-shell">
      <aside className="sidebar" aria-label="PasteBox navigation">
        <div className="brand-mark">
          <div className="brand-icon">
            <ClipboardCopy size={22} aria-hidden="true" />
          </div>
          <div>
            <strong>PasteBox</strong>
            <span>{user.email}</span>
          </div>
        </div>

        <nav className="nav-list">
          <button
            className={navClass(view, "inbox")}
            type="button"
            onClick={() => setView("inbox")}
          >
            <Archive size={18} aria-hidden="true" />
            {t("inbox")}
          </button>
          <button
            className={navClass(view, "shared")}
            type="button"
            onClick={() => setView("shared")}
          >
            <Link2 size={18} aria-hidden="true" />
            {t("shares")}
          </button>
          <button
            className={navClass(view, "billing")}
            type="button"
            onClick={() => setView("billing")}
          >
            <CreditCard size={18} aria-hidden="true" />
            {t("billing")}
          </button>
          <button
            className={navClass(view, "settings")}
            type="button"
            onClick={() => setView("settings")}
          >
            <UserRound size={18} aria-hidden="true" />
            {t("settings")}
          </button>
          {user.role === "admin" ? (
            <button
              className={navClass(view, "admin")}
              type="button"
              onClick={() => {
                setView("admin");
                void refreshAdmin();
              }}
            >
              <ShieldCheck size={18} aria-hidden="true" />
              {t("admin")}
            </button>
          ) : null}
        </nav>

        <section className="quota-panel" aria-label={t("currentPlan")}>
          <div>
            <span className="eyebrow">{t("currentPlan")}</span>
            <strong>{activePlan?.name ?? user.planId}</strong>
          </div>
          <div className="quota-bar">
            <span
              style={{
                width: `${storagePercent}%`,
              }}
            />
          </div>
          <p>
            {formatBytes(storageUsed)} / {formatBytes(storageLimit)} ·{" "}
            {quota?.activePasteCount ?? 0}/{activePlan?.activePasteLimit ?? 0}{" "}
            {t("pastes")}
          </p>
        </section>

        <button className="ghost-button" type="button" onClick={logout}>
          <LogOut size={16} aria-hidden="true" />
          {t("logout")}
        </button>
      </aside>

      <section className="workspace">
        <header className="topbar">
          <label className="search-box">
            <Search size={18} aria-hidden="true" />
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              type="search"
              placeholder={t("search")}
            />
          </label>
          <label className="select-box">
            <Filter size={18} aria-hidden="true" />
            <select
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
            >
              <option value="all">{t("all")}</option>
              <option value="text">{t("text")}</option>
              <option value="image">{t("images")}</option>
              <option value="file">{t("files")}</option>
              <option value="expiring">{t("expiring")}</option>
              <option value="shared">{t("shared")}</option>
              <option value="favorite">{t("favorites")}</option>
            </select>
          </label>
          {tagFilter ? (
            <button
              className="status-pill tag-filter-pill"
              type="button"
              onClick={() => setTagFilter("")}
              aria-label={`${t("clearTagFilter")}: ${tagFilter}`}
            >
              <Tags size={14} aria-hidden="true" />
              <span>
                {t("filteredByTag")}: {tagFilter}
              </span>
            </button>
          ) : null}
          {message ? <span className="status-pill">{message}</span> : null}
        </header>

        <section className="workspace-hero" aria-label="Workspace overview">
          <div className="workspace-hero-copy">
            <span className="eyebrow">{viewSummary.eyebrow}</span>
            <h1>{viewSummary.title}</h1>
            <p>{viewSummary.description}</p>
          </div>
          <div className="workspace-stat-grid">
            {workspaceStats.map((stat) => (
              <article
                className={`workspace-stat workspace-stat--${stat.tone}`}
                key={stat.label}
              >
                <span>{stat.label}</span>
                <strong>{stat.value}</strong>
                <small>{stat.detail}</small>
              </article>
            ))}
          </div>
        </section>

        {!user.emailVerified ? (
          <section className="verify-banner">
            <div>
              <strong>{t("emailVerificationRequired")}</strong>
              <span>{user.email}</span>
            </div>
            <input
              value={verificationToken}
              onChange={(event) => setVerificationToken(event.target.value)}
              placeholder={t("verificationToken")}
            />
            <button type="button" onClick={startVerification} disabled={busy}>
              <Send size={16} aria-hidden="true" />
              {t("send")}
            </button>
            <button
              type="button"
              onClick={finishVerification}
              disabled={busy || !verificationToken}
            >
              <MailCheck size={16} aria-hidden="true" />
              {t("verify")}
            </button>
          </section>
        ) : null}

        {view === "inbox" ? (
          <>
            <section className="composer" aria-labelledby="new-paste-title">
              <div className="composer-heading">
                <div>
                  <span className="eyebrow">{t("newPrivatePaste")}</span>
                  <h1 id="new-paste-title">{t("createSecureDrop")}</h1>
                </div>
                <div className="privacy-badge">
                  <LockKeyhole size={16} aria-hidden="true" />
                  {t("private")}
                </div>
              </div>
              <SendModeTabs
                ariaLabel={t("newPrivatePaste")}
                className="send-mode-tabs"
                idPrefix="send"
                mode={sendMode}
                onSelect={switchSendMode}
                labels={transferLabels}
              />
              <div
                aria-labelledby={`send-tab-${sendMode}`}
                className="send-panel"
                id={`send-panel-${sendMode}`}
                onPaste={(event) => {
                  const pasted = Array.from(event.clipboardData.files);
                  if (pasted.length === 0) return;
                  if (sendMode === "image") {
                    void stageSendImage(pasted);
                  } else if (sendMode === "file") {
                    stageSendFiles(pasted);
                  }
                }}
                role="tabpanel"
              >
                <input
                  className="title-input"
                  value={draft.title}
                  onChange={(event) =>
                    setDraft({ ...draft, title: event.target.value })
                  }
                  placeholder={t("titleThisPaste")}
                />
                {sendMode === "text" ? (
                  <textarea
                    value={draft.text}
                    onChange={(event) =>
                      setDraft({ ...draft, text: event.target.value })
                    }
                    placeholder={t("pasteTextPlaceholder")}
                  />
                ) : null}
                <div className="composer-controls">
                  <label className="tag-input-wrap">
                    <Tags size={16} aria-hidden="true" />
                    <input
                      value={draft.tags}
                      onChange={(event) =>
                        setDraft({ ...draft, tags: event.target.value })
                      }
                      placeholder={
                        canCreateTags
                          ? t("tagsSeparatedByComma")
                          : t("upgradeForTags")
                      }
                      disabled={!canCreateTags}
                    />
                    <span className="tag-limit-note">
                      {canCreateTags
                        ? `${draftTagCount}/${tagsPerPasteLimit}`
                        : t("upgradeForTags")}
                    </span>
                  </label>
                  {sendMode === "text" ? (
                    <button type="button" onClick={createPaste} disabled={busy}>
                      <Sparkles size={16} aria-hidden="true" />
                      {transferLabels.createRecord}
                    </button>
                  ) : null}
                </div>
                <SendSettingsFields
                  expiresInSeconds={draft.expiresInSeconds}
                  expiryOptions={sendExpiryOptions}
                  labels={transferLabels}
                  loginRequired={sendSettings.loginRequired}
                  onExpiry={(seconds) =>
                    setDraft({ ...draft, expiresInSeconds: seconds })
                  }
                  onLoginRequired={(value) =>
                    setSendSettings({ ...sendSettings, loginRequired: value })
                  }
                  onPassword={(value) =>
                    setSendSettings({ ...sendSettings, password: value })
                  }
                  password={sendSettings.password}
                />
                {sendMode !== "text" ? (
                  <>
                    <label
                      aria-disabled={!activeSendQueue.canStage}
                      className={`drop-zone ${activeSendQueue.canStage ? "" : "drop-zone--locked"}`}
                      htmlFor={
                        sendMode === "image" ? imageInputId : attachmentInputId
                      }
                      onDragOver={(event) => event.preventDefault()}
                      onDrop={(event) => {
                        event.preventDefault();
                        const dropped = Array.from(event.dataTransfer.files);
                        if (sendMode === "image") {
                          void stageSendImage(dropped);
                        } else {
                          stageSendFiles(dropped);
                        }
                      }}
                    >
                      <UploadCloud size={20} aria-hidden="true" />
                      <input
                        accept={sendMode === "image" ? "image/*" : undefined}
                        className="visually-hidden-file-input"
                        disabled={!activeSendQueue.canStage}
                        id={
                          sendMode === "image"
                            ? imageInputId
                            : attachmentInputId
                        }
                        multiple={sendMode === "file"}
                        type="file"
                        onChange={(event) => {
                          const files = Array.from(event.target.files ?? []);
                          event.target.value = "";
                          if (sendMode === "image") {
                            void stageSendImage(files);
                          } else {
                            stageSendFiles(files);
                          }
                        }}
                      />
                      {activeSendQueue.canStage
                        ? `${sendMode === "image" ? transferLabels.chooseImage : transferLabels.sendFile} · ${sendMode === "image" ? transferLabels.dropImageHint : transferLabels.dropFilesHint}`
                        : sendZoneLabel(activeSendQueue.phase, transferLabels)}
                    </label>
                    <SendQueueList
                      canRemove={activeSendQueue.canStage}
                      items={activeSendQueue.items}
                      labels={transferLabels}
                      onRemove={activeSendQueue.removeFile}
                    />
                  </>
                ) : null}
                <div className="button-row compact">
                  {sendMode === "file" ? (
                    <>
                      <button
                        type="button"
                        onClick={() =>
                          document.getElementById(attachInputId)?.click()
                        }
                      >
                        <Link2 size={16} aria-hidden="true" />
                        {transferLabels.attachToSelected}
                      </button>
                      <input
                        className="visually-hidden-file-input"
                        id={attachInputId}
                        type="file"
                        onChange={(event) => {
                          const file = event.target.files?.[0];
                          event.target.value = "";
                          if (file) void attachFile(file);
                        }}
                      />
                    </>
                  ) : null}
                  {sendMode === "text" ? (
                    <button
                      type="button"
                      disabled={
                        draft.text.trim() === "" || textSend.phase === "sending"
                      }
                      onClick={sendTextDraft}
                    >
                      <Link2 size={16} aria-hidden="true" />
                      {textSend.phase === "sending"
                        ? transferLabels.sendingText
                        : transferLabels.sendText}
                    </button>
                  ) : null}
                  {sendMode !== "text" && activeSendQueue.canSend ? (
                    <button type="button" onClick={activeSendQueue.start}>
                      <UploadCloud size={16} aria-hidden="true" />
                      {fillCopy(transferLabels.sendFiles, {
                        count: activeSendQueue.items.length,
                      })}
                    </button>
                  ) : null}
                  {sendMode !== "text" && activeSendQueue.phase === "failed" ? (
                    <button type="button" onClick={activeSendQueue.retry}>
                      <RotateCcw size={16} aria-hidden="true" />
                      {activeSendQueue.counts.failed > 0
                        ? transferLabels.retryFailed
                        : transferLabels.retrySend}
                    </button>
                  ) : null}
                  {sendMode === "text" && textSend.phase === "failed" ? (
                    <button type="button" onClick={sendTextDraft}>
                      <RotateCcw size={16} aria-hidden="true" />
                      {transferLabels.retrySend}
                    </button>
                  ) : null}
                  {sendMode !== "text" &&
                  (activeSendQueue.phase === "uploading" ||
                    activeSendQueue.phase === "publishing" ||
                    activeSendQueue.phase === "failed") ? (
                    <button
                      type="button"
                      onClick={() => void cancelSend(activeSendQueue)}
                    >
                      <Ban size={16} aria-hidden="true" />
                      {transferLabels.cancelSend}
                    </button>
                  ) : null}
                </div>
                {sendMode === "text" ? (
                  textSendStatusText ? (
                    <div className="send-status">
                      <p className="status-line">{textSendStatusText}</p>
                    </div>
                  ) : null
                ) : activeSendStatusText ? (
                  <div className="send-status">
                    <p className="status-line">{activeSendStatusText}</p>
                  </div>
                ) : null}
                {sendShare ? (
                  <SendShareResult
                    copyLabel={t("copy")}
                    labels={transferLabels}
                    lineClassName="status-line"
                    onCopy={(value) =>
                      void navigator.clipboard?.writeText(value)
                    }
                    onReset={
                      sendMode === "text"
                        ? textSend.reset
                        : activeSendQueue.reset
                    }
                    resetLabel={transferLabels.newSend}
                    share={sendShare}
                    wrapperClassName="send-status"
                  />
                ) : null}
              </div>
            </section>

            <section className="content-grid">
              <PasteList
                pastes={pastes}
                selectedId={selectedPaste?.id ?? ""}
                onSelect={setSelectedPasteId}
                onCopy={(text) => void navigator.clipboard?.writeText(text)}
                onDelete={async (id) => {
                  await run(() => client.deletePaste(id), t("pasteDeleted"));
                  await refreshAuthed();
                }}
                onExtend={(paste, seconds) => void extendPaste(paste, seconds)}
                onToggleFlag={(paste, field) =>
                  void updatePasteFlag(paste, field)
                }
                onFilterTag={setTagFilter}
                activeTag={tagFilter}
                locale={locale}
              />
              <aside className="side-panel">
                <PasteEditor
                  paste={selectedPaste}
                  draft={editDraft}
                  onDraft={setEditDraft}
                  onSave={saveSelectedPaste}
                  tagLimit={tagsPerPasteLimit}
                  canEditTags={canEditSelectedTags}
                  tagsReadOnly={selectedTagsReadOnly}
                  locale={locale}
                />
                <ShareBox
                  paste={selectedPaste}
                  draft={shareDraft}
                  token={shareToken}
                  onDraft={setShareDraft}
                  onCreate={createShare}
                  onOpen={() => void openShare()}
                  access={shareAccess}
                  locale={locale}
                />
              </aside>
            </section>
          </>
        ) : null}

        {view === "shared" ? (
          <Panel
            title={t("shares")}
            meta={`${shares.length} ${t("sharedLinks")}`}
          >
            {shares.map((share) => (
              <article className="list-card" key={share.id}>
                <div>
                  <strong>{share.url}</strong>
                  <span>
                    {share.visitCount}/{share.maxVisits || "∞"} {t("visits")} ·{" "}
                    {share.downloadCount}/{share.maxDownloads || "∞"}{" "}
                    {t("downloads")}
                  </span>
                  <span>
                    {share.loginRequired ? t("loginRequired") : t("anonymous")}{" "}
                    ·{t("expires")} {new Date(share.expiresAt).toLocaleString()}
                  </span>
                </div>
                <button
                  type="button"
                  onClick={() => {
                    void run(
                      () => client.revokeShare(share.id),
                      "Share revoked",
                    ).then(refreshAuthed);
                  }}
                  disabled={Boolean(share.revokedAt)}
                >
                  {t("revoke")}
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setReportDraft({
                      target: `share:${share.id}`,
                      reason: reportDraft.reason,
                    });
                    void submitReport(`share:${share.id}`);
                  }}
                >
                  {t("report")}
                </button>
              </article>
            ))}
          </Panel>
        ) : null}

        {view === "billing" ? (
          <Panel title={t("billing")} meta={t("billingStatusTitle")}>
            <section className="notice-card">
              <LifeBuoy size={18} aria-hidden="true" />
              <div>
                <strong>{t("billingSupport")}</strong>
                <span>
                  {t("billingSupportBody")}{" "}
                  <a href="/legal/refund">{t("refundPolicy")}</a>{" "}
                  <a href="/support">{t("support")}</a>.
                </span>
              </div>
            </section>
            <div className="plan-grid">
              {(catalog?.plans ?? []).map((plan) => (
                <article className="plan-card" key={plan.id}>
                  <strong>{plan.name}</strong>
                  <span>
                    {plan.activePasteLimit.toLocaleString()} {t("activePastes")}
                  </span>
                  <dl>
                    <div>
                      <dt>{t("storage")}</dt>
                      <dd>{formatBytes(plan.activeStorageBytes)}</dd>
                    </div>
                    <div>
                      <dt>{t("file")}</dt>
                      <dd>{formatBytes(plan.singleFileBytes)}</dd>
                    </div>
                    <div>
                      <dt>{t("retention")}</dt>
                      <dd>{formatDuration(plan.maxRetentionSeconds)}</dd>
                    </div>
                    <div>
                      <dt>{t("traffic")}</dt>
                      <dd>{formatBytes(plan.dailyShareDownloadBytes)}</dd>
                    </div>
                  </dl>
                  <div className="price-list">
                    {(pricesByPlan.get(plan.id) ?? []).flatMap((price) => {
                      const providers = paymentProviderOptions(price);
                      if (providers.length === 0) {
                        return [
                          <button type="button" key={price.id} disabled>
                            {t("unavailable")} · {price.period} ·{" "}
                            {(price.amountCents / 100).toFixed(2)}{" "}
                            {price.currency}
                          </button>,
                        ];
                      }
                      return providers.map((option) => (
                        <button
                          type="button"
                          key={`${price.id}-${option.provider}`}
                          onClick={() =>
                            void makeOrder(
                              plan.id,
                              price.period,
                              option.provider,
                            )
                          }
                        >
                          {option.label} · {price.period} ·{" "}
                          {(price.amountCents / 100).toFixed(2)}{" "}
                          {price.currency}
                        </button>
                      ));
                    })}
                  </div>
                </article>
              ))}
            </div>
            {orders.map((order) => {
              const status = orderStatusDetail(order.status, locale);
              return (
                <article className="list-card" key={order.id}>
                  <div>
                    <strong>{order.planId}</strong>
                    <span>
                      {order.provider} · {(order.amountCents / 100).toFixed(2)}{" "}
                      {order.currency}
                    </span>
                    <span className="order-status-note">
                      {status.description}
                    </span>
                    {order.status === "pending" ? (
                      <div className="order-payment-info">
                        {order.checkoutUrl ? (
                          <a
                            href={order.checkoutUrl}
                            rel="noreferrer"
                            target="_blank"
                          >
                            {t("openCheckout")}
                          </a>
                        ) : null}
                        {order.address ? (
                          <span>
                            {order.chain || "USDT"} {t("paymentAddress")}:{" "}
                            <code>{order.address}</code>
                          </span>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                  <span className={`order-status order-status--${status.tone}`}>
                    {status.label}
                  </span>
                </article>
              );
            })}
          </Panel>
        ) : null}

        {view === "settings" ? (
          <Panel
            title={t("settings")}
            meta={
              user.deleteScheduledAt
                ? t("deletionScheduled")
                : t("accountActive")
            }
          >
            <section className="notice-card">
              <FileText size={18} aria-hidden="true" />
              <div>
                <strong>{t("accountRights")}</strong>
                <span>
                  {t("accountRightsBody")}{" "}
                  <a href="/legal/data-export">{t("dataExport")}</a>{" "}
                  <a href="/legal/account-deletion">{t("accountDeletion")}</a>{" "}
                  <a href="/legal/privacy">{t("privacy")}</a>{" "}
                  <a href="/support">{t("supportIntake")}</a>
                </span>
              </div>
            </section>
            <section className="notice-card">
              <ShieldCheck size={18} aria-hidden="true" />
              <div>
                <strong>{t("linkedAccounts")}</strong>
                {linkedOAuthProviders.length > 0 ? (
                  <span>
                    {linkedOAuthProviders
                      .map((provider) =>
                        provider === "google" ? t("google") : provider,
                      )
                      .join(", ")}
                  </span>
                ) : (
                  <span>{t("noLinkedAccounts")}</span>
                )}
                {linkedOAuthProviders.includes("google") ? (
                  <button
                    type="button"
                    onClick={() => unlinkOAuth("google")}
                    disabled={busy}
                  >
                    {t("unlinkGoogle")}
                  </button>
                ) : null}
              </div>
            </section>
            <div className="form-grid">
              <input
                value={profileDraft.displayName}
                onChange={(event) =>
                  setProfileDraft({
                    ...profileDraft,
                    displayName: event.target.value,
                  })
                }
                placeholder={t("displayName")}
              />
              <select
                value={profileDraft.language}
                onChange={(event) =>
                  setProfileDraft({
                    ...profileDraft,
                    language: localeFor(event.target.value),
                  })
                }
              >
                {supportedLocales.map((option) => (
                  <option value={option.value} key={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
              <button type="button" onClick={updateProfile} disabled={busy}>
                {t("saveProfile")}
              </button>
            </div>
            <section className="notice-card session-card">
              <LogOut size={18} aria-hidden="true" />
              <div>
                <strong>{t("logoutAllDevices")}</strong>
                <span>{t("logoutAllDevicesDescription")}</span>
                <button type="button" onClick={logoutAll} disabled={busy}>
                  <LogOut size={16} aria-hidden="true" />
                  {t("logoutAllDevices")}
                </button>
              </div>
            </section>
            <div className="button-row">
              <button type="button" onClick={exportData}>
                <Download size={16} aria-hidden="true" />
                {t("export")}
              </button>
              <button type="button" onClick={requestDelete}>
                <Trash2 size={16} aria-hidden="true" />
                {t("deleteRequest")}
              </button>
              <button
                type="button"
                onClick={cancelDelete}
                disabled={!user.deleteScheduledAt}
              >
                {t("cancelDelete")}
              </button>
              <button
                type="button"
                onClick={executeDelete}
                disabled={!user.deleteScheduledAt}
              >
                {t("deleteNow")}
              </button>
            </div>
            <div className="form-grid">
              <input
                value={reportDraft.target}
                onChange={(event) =>
                  setReportDraft({
                    ...reportDraft,
                    target: event.target.value,
                  })
                }
                placeholder={t("reportTarget")}
              />
              <input
                value={reportDraft.reason}
                onChange={(event) =>
                  setReportDraft({
                    ...reportDraft,
                    reason: event.target.value,
                  })
                }
                placeholder={t("reportReason")}
              />
              <button
                type="button"
                onClick={() => void submitReport()}
                disabled={!reportDraft.target}
              >
                <Send size={16} aria-hidden="true" />
                {t("report")}
              </button>
            </div>
          </Panel>
        ) : null}

        {view === "admin" ? (
          <Panel
            className="admin-panel"
            title={t("admin")}
            meta={t("auditQueuesCleanup")}
          >
            <div className="admin-console">
              <div className="metric-grid admin-metric-grid">
                {Object.entries(adminStats ?? {}).map(([key, value]) => (
                  <div className="metric" key={key}>
                    <span>{adminMetricLabel(key, t)}</span>
                    <strong>{adminMetricValue(key, value)}</strong>
                  </div>
                ))}
              </div>
              <div className="button-row admin-command-row">
                <button type="button" onClick={runCleanup}>
                  <RotateCcw size={16} aria-hidden="true" />
                  {t("runCleanup")}
                </button>
                <button type="button" onClick={runBillingReconciliation}>
                  <CreditCard size={16} aria-hidden="true" />
                  {t("runBillingReconcile")}
                </button>
              </div>
              <div
                className="admin-tabs"
                role="tablist"
                aria-label={t("admin")}
              >
                {adminTabOptions.map((tab) => (
                  <button
                    key={tab.value}
                    type="button"
                    role="tab"
                    aria-selected={adminTab === tab.value}
                    className={adminTab === tab.value ? "active" : ""}
                    onClick={() => setAdminTab(tab.value)}
                  >
                    {t(tab.labelKey)}
                  </button>
                ))}
              </div>
              <div className={`admin-grid admin-grid--${adminTab}`}>
                {adminTab === "configuration" && adminData.managedConfig ? (
                  <ManagedConfigEditor
                    busy={busy}
                    saveResults={saveResults}
                    secretDraft={managedSecretDraft}
                    t={t}
                    view={adminData.managedConfig}
                    onChange={updateManagedConfig}
                    onSave={(group) => void saveManagedConfig(group)}
                    onSecretChange={updateManagedSecret}
                  />
                ) : null}
                {adminTab === "guest" || adminTab === "services" ? (
                  <section className="admin-section admin-section--wide">
                    <h3>
                      {adminTab === "guest"
                        ? t("guestUploads")
                        : t("telegramAlerts")}
                    </h3>
                    {adminTab === "guest" ? (
                      <article className="list-card">
                        <strong>{t("guestUploads")}</strong>
                        <div className="form-grid">
                          <label className="check-row">
                            <input
                              checked={
                                adminData.runtimeConfig?.guestUploads.enabled ??
                                false
                              }
                              type="checkbox"
                              onChange={() => void toggleGuestUploads()}
                            />
                            {t("enabled")}
                          </label>
                          <label className="check-row">
                            <input
                              checked={
                                adminData.runtimeConfig?.guestUploads
                                  .requireTurnstile ?? false
                              }
                              type="checkbox"
                              onChange={(event) =>
                                updateRuntimeGuestConfig({
                                  requireTurnstile: event.target.checked,
                                })
                              }
                            />
                            {t("requireTurnstile")}
                          </label>
                          <AdminTimeField
                            label={t("retentionSeconds")}
                            minSeconds={1}
                            t={t}
                            unit={adminTimeUnitFor(
                              "guest.retentionSeconds",
                              adminData.runtimeConfig?.guestUploads
                                .retentionSeconds ?? 0,
                            )}
                            valueSeconds={
                              adminData.runtimeConfig?.guestUploads
                                .retentionSeconds ?? 0
                            }
                            onChangeSeconds={(value) =>
                              updateRuntimeGuestConfig({
                                retentionSeconds: value,
                              })
                            }
                            onUnitChange={(unit) =>
                              setAdminTimeUnit("guest.retentionSeconds", unit)
                            }
                          />
                          <AdminNumberField
                            label={t("activePasteLimitShort")}
                            min={1}
                            unitLabel={t("unitItems")}
                            value={
                              adminData.runtimeConfig?.guestUploads
                                .activePasteLimit ?? 0
                            }
                            onChange={(value) =>
                              updateRuntimeGuestConfig({
                                activePasteLimit: value,
                              })
                            }
                          />
                          <AdminSizeField
                            label={t("activeStorageLimit")}
                            minBytes={1}
                            t={t}
                            unit={adminSizeUnitFor(
                              "guest.activeStorageBytes",
                              adminData.runtimeConfig?.guestUploads
                                .activeStorageBytes ?? 0,
                            )}
                            valueBytes={
                              adminData.runtimeConfig?.guestUploads
                                .activeStorageBytes ?? 0
                            }
                            onChangeBytes={(value) =>
                              updateRuntimeGuestConfig({
                                activeStorageBytes: value,
                              })
                            }
                            onUnitChange={(unit) =>
                              setAdminSizeUnit("guest.activeStorageBytes", unit)
                            }
                          />
                          <AdminSizeField
                            label={t("singleTextLimit")}
                            minBytes={1}
                            t={t}
                            unit={adminSizeUnitFor(
                              "guest.singleTextBytes",
                              adminData.runtimeConfig?.guestUploads
                                .singleTextBytes ?? 0,
                            )}
                            valueBytes={
                              adminData.runtimeConfig?.guestUploads
                                .singleTextBytes ?? 0
                            }
                            onChangeBytes={(value) =>
                              updateRuntimeGuestConfig({
                                singleTextBytes: value,
                              })
                            }
                            onUnitChange={(unit) =>
                              setAdminSizeUnit("guest.singleTextBytes", unit)
                            }
                          />
                          <AdminSizeField
                            label={t("singleFileLimit")}
                            minBytes={1}
                            t={t}
                            unit={adminSizeUnitFor(
                              "guest.singleFileBytes",
                              adminData.runtimeConfig?.guestUploads
                                .singleFileBytes ?? 0,
                            )}
                            valueBytes={
                              adminData.runtimeConfig?.guestUploads
                                .singleFileBytes ?? 0
                            }
                            onChangeBytes={(value) =>
                              updateRuntimeGuestConfig({
                                singleFileBytes: value,
                              })
                            }
                            onUnitChange={(unit) =>
                              setAdminSizeUnit("guest.singleFileBytes", unit)
                            }
                          />
                          <AdminSizeField
                            label={t("singlePasteLimit")}
                            minBytes={1}
                            t={t}
                            unit={adminSizeUnitFor(
                              "guest.singlePasteBytes",
                              adminData.runtimeConfig?.guestUploads
                                .singlePasteBytes ?? 0,
                            )}
                            valueBytes={
                              adminData.runtimeConfig?.guestUploads
                                .singlePasteBytes ?? 0
                            }
                            onChangeBytes={(value) =>
                              updateRuntimeGuestConfig({
                                singlePasteBytes: value,
                              })
                            }
                            onUnitChange={(unit) =>
                              setAdminSizeUnit("guest.singlePasteBytes", unit)
                            }
                          />
                          <AdminNumberField
                            label={t("attachmentsPerPaste")}
                            min={1}
                            unitLabel={t("unitFiles")}
                            value={
                              adminData.runtimeConfig?.guestUploads
                                .attachmentsPerPasteLimit ?? 0
                            }
                            onChange={(value) =>
                              updateRuntimeGuestConfig({
                                attachmentsPerPasteLimit: value,
                              })
                            }
                          />
                          <AdminSizeField
                            label={t("dailyUploadLimit")}
                            minBytes={1}
                            t={t}
                            unit={adminSizeUnitFor(
                              "guest.dailyUploadBytes",
                              adminData.runtimeConfig?.guestUploads
                                .dailyUploadBytes ?? 0,
                            )}
                            valueBytes={
                              adminData.runtimeConfig?.guestUploads
                                .dailyUploadBytes ?? 0
                            }
                            onChangeBytes={(value) =>
                              updateRuntimeGuestConfig({
                                dailyUploadBytes: value,
                              })
                            }
                            onUnitChange={(unit) =>
                              setAdminSizeUnit("guest.dailyUploadBytes", unit)
                            }
                          />
                          <AdminSizeField
                            label={t("dailyShareDownloadLimit")}
                            minBytes={0}
                            t={t}
                            unit={adminSizeUnitFor(
                              "guest.dailyShareDownloadBytes",
                              adminData.runtimeConfig?.guestUploads
                                .dailyShareDownloadBytes ?? 0,
                            )}
                            valueBytes={
                              adminData.runtimeConfig?.guestUploads
                                .dailyShareDownloadBytes ?? 0
                            }
                            onChangeBytes={(value) =>
                              updateRuntimeGuestConfig({
                                dailyShareDownloadBytes: value,
                              })
                            }
                            onUnitChange={(unit) =>
                              setAdminSizeUnit(
                                "guest.dailyShareDownloadBytes",
                                unit,
                              )
                            }
                          />
                        </div>
                      </article>
                    ) : null}
                    {adminTab === "services" ? (
                      <article className="list-card">
                        <strong>{t("processLogs")}</strong>
                        <div className="form-grid">
                          <label className="field-row">
                            <span>{t("logLevel")}</span>
                            <select
                              value={
                                adminData.runtimeConfig?.logLevel ?? "info"
                              }
                              onChange={(event) =>
                                updateRuntimeLogLevel(
                                  event.target.value as LogLevel,
                                )
                              }
                            >
                              {logLevelOptions.map((level) => (
                                <option key={level} value={level}>
                                  {t(
                                    `logLevel${
                                      level.charAt(0).toUpperCase() +
                                      level.slice(1)
                                    }`,
                                  )}
                                </option>
                              ))}
                            </select>
                          </label>
                        </div>
                      </article>
                    ) : null}
                    {adminTab === "services" ? (
                      <article className="list-card">
                        <strong>{t("telegramAlerts")}</strong>
                        <div className="form-grid">
                          <label className="check-row">
                            <input
                              checked={
                                adminData.runtimeConfig?.alerts.enabled ?? false
                              }
                              type="checkbox"
                              onChange={() => void toggleRuntimeAlerts()}
                            />
                            {t("enabled")}
                          </label>
                          <label className="check-row">
                            <input
                              checked={
                                adminData.runtimeConfig?.alerts
                                  .telegramEnabled ?? false
                              }
                              type="checkbox"
                              onChange={(event) =>
                                updateRuntimeAlertConfig({
                                  telegramEnabled: event.target.checked,
                                })
                              }
                            />
                            {t("telegramDelivery")}
                          </label>
                          <label className="check-row">
                            <input
                              checked={
                                adminData.runtimeConfig?.alerts.silent ?? false
                              }
                              type="checkbox"
                              onChange={(event) =>
                                updateRuntimeAlertConfig({
                                  silent: event.target.checked,
                                })
                              }
                            />
                            {t("silentAlert")}
                          </label>
                          <AdminTimeField
                            label={t("cooldownSeconds")}
                            minSeconds={1}
                            t={t}
                            unit={adminTimeUnitFor(
                              "alerts.cooldownSeconds",
                              adminData.runtimeConfig?.alerts.cooldownSeconds ??
                                0,
                            )}
                            valueSeconds={
                              adminData.runtimeConfig?.alerts.cooldownSeconds ??
                              0
                            }
                            onChangeSeconds={(value) =>
                              updateRuntimeAlertConfig({
                                cooldownSeconds: value,
                              })
                            }
                            onUnitChange={(unit) =>
                              setAdminTimeUnit("alerts.cooldownSeconds", unit)
                            }
                          />
                          <AdminNumberField
                            label={t("cpuThreshold")}
                            min={1}
                            step={0.1}
                            unitLabel={t("unitPercent")}
                            value={
                              adminData.runtimeConfig?.alerts
                                .cpuPercentThreshold ?? 0
                            }
                            onChange={(value) =>
                              updateRuntimeAlertConfig({
                                cpuPercentThreshold: value,
                              })
                            }
                          />
                          <AdminNumberField
                            label={t("memoryThreshold")}
                            min={1}
                            step={0.1}
                            unitLabel={t("unitPercent")}
                            value={
                              adminData.runtimeConfig?.alerts
                                .memoryPercentThreshold ?? 0
                            }
                            onChange={(value) =>
                              updateRuntimeAlertConfig({
                                memoryPercentThreshold: value,
                              })
                            }
                          />
                          <AdminNumberField
                            label={t("diskThreshold")}
                            min={1}
                            step={0.1}
                            unitLabel={t("unitPercent")}
                            value={
                              adminData.runtimeConfig?.alerts
                                .diskPercentThreshold ?? 0
                            }
                            onChange={(value) =>
                              updateRuntimeAlertConfig({
                                diskPercentThreshold: value,
                              })
                            }
                          />
                          <AdminSizeField
                            label={t("objectStorageThreshold")}
                            minBytes={0}
                            t={t}
                            unit={adminSizeUnitFor(
                              "alerts.objectStorageBytesThreshold",
                              adminData.runtimeConfig?.alerts
                                .objectStorageBytesThreshold ?? 0,
                            )}
                            valueBytes={
                              adminData.runtimeConfig?.alerts
                                .objectStorageBytesThreshold ?? 0
                            }
                            onChangeBytes={(value) =>
                              updateRuntimeAlertConfig({
                                objectStorageBytesThreshold: value,
                              })
                            }
                            onUnitChange={(unit) =>
                              setAdminSizeUnit(
                                "alerts.objectStorageBytesThreshold",
                                unit,
                              )
                            }
                          />
                          <AdminNumberField
                            label={t("scanFailureThreshold")}
                            min={0}
                            unitLabel={t("unitItems")}
                            value={
                              adminData.runtimeConfig?.alerts
                                .scanFailureDepthThreshold ?? 0
                            }
                            onChange={(value) =>
                              updateRuntimeAlertConfig({
                                scanFailureDepthThreshold: value,
                              })
                            }
                          />
                          <AdminNumberField
                            label={t("failedJobThreshold")}
                            min={0}
                            unitLabel={t("unitItems")}
                            value={
                              adminData.runtimeConfig?.alerts
                                .failedJobDepthThreshold ?? 0
                            }
                            onChange={(value) =>
                              updateRuntimeAlertConfig({
                                failedJobDepthThreshold: value,
                              })
                            }
                          />
                          <AdminNumberField
                            label={t("failedMailThreshold")}
                            min={0}
                            unitLabel={t("unitItems")}
                            value={
                              adminData.runtimeConfig?.alerts
                                .mailFailedDepthThreshold ?? 0
                            }
                            onChange={(value) =>
                              updateRuntimeAlertConfig({
                                mailFailedDepthThreshold: value,
                              })
                            }
                          />
                          <AdminNumberField
                            label={t("openReportThreshold")}
                            min={0}
                            unitLabel={t("unitItems")}
                            value={
                              adminData.runtimeConfig?.alerts
                                .reportsOpenThreshold ?? 0
                            }
                            onChange={(value) =>
                              updateRuntimeAlertConfig({
                                reportsOpenThreshold: value,
                              })
                            }
                          />
                        </div>
                      </article>
                    ) : null}
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => void saveRuntimeConfig()}
                    >
                      <ShieldCheck size={16} aria-hidden="true" />
                      {t("saveRuntimeConfig")}
                    </button>
                    <SaveFeedback result={saveResults[`runtime-${adminTab}`]} />
                  </section>
                ) : null}
                {adminTab === "security" ? (
                  <section className="admin-section admin-section--wide">
                    <h3>{t("registrationSecurity")}</h3>
                    <article className="list-card">
                      <strong>{t("registrationSecurity")}</strong>
                      <div className="form-grid">
                        <label className="check-row">
                          <input
                            checked={
                              adminData.runtimeConfig?.registration
                                .requireEmailVerification ?? false
                            }
                            type="checkbox"
                            onChange={(event) =>
                              updateRuntimeRegistrationConfig({
                                requireEmailVerification: event.target.checked,
                              })
                            }
                          />
                          {t("requireEmailVerification")}
                        </label>
                        <label className="check-row">
                          <input
                            checked={
                              adminData.runtimeConfig?.registration
                                .requireTurnstile ?? false
                            }
                            type="checkbox"
                            onChange={(event) =>
                              updateRuntimeRegistrationConfig({
                                requireTurnstile: event.target.checked,
                              })
                            }
                          />
                          {t("requireTurnstile")}
                        </label>
                        <label className="field-row field-row--wide">
                          <span>{t("allowedEmailDomains")}</span>
                          <textarea
                            placeholder={t("allowedEmailDomainsPlaceholder")}
                            value={domainListText(
                              adminData.runtimeConfig?.registration
                                .allowedDomains ?? [],
                            )}
                            onChange={(event) =>
                              updateRuntimeRegistrationConfig({
                                allowedDomains: parseDomainList(
                                  event.target.value,
                                ),
                              })
                            }
                          />
                        </label>
                        {adminData.managedConfig ? (
                          <>
                            <ManagedTextField
                              label={t("turnstileSiteKey")}
                              value={
                                adminData.managedConfig.config.turnstile.siteKey
                              }
                              onChange={(siteKey) =>
                                updateManagedTurnstile({ siteKey })
                              }
                            />
                            <ManagedSecretField
                              label={t("turnstileSecretKey")}
                              name="turnstileSecretKey"
                              configured={
                                adminData.managedConfig.secrets
                                  .turnstileSecretKey
                              }
                              draft={managedSecretDraft}
                              t={t}
                              onChange={updateManagedSecret}
                            />
                          </>
                        ) : null}
                      </div>
                    </article>
                    <article className="list-card">
                      <strong>{t("rateLimits")}</strong>
                      <div className="form-grid">
                        <label className="check-row">
                          <input
                            checked={
                              adminData.runtimeConfig?.rateLimits.enabled ??
                              false
                            }
                            type="checkbox"
                            onChange={(event) =>
                              updateRuntimeRateLimitConfig({
                                enabled: event.target.checked,
                              })
                            }
                          />
                          {t("enabled")}
                        </label>
                        <AdminTimeField
                          label={t("rateLimitWindow")}
                          minSeconds={1}
                          t={t}
                          unit={adminTimeUnitFor(
                            "rate.windowSeconds",
                            adminData.runtimeConfig?.rateLimits.windowSeconds ??
                              0,
                          )}
                          valueSeconds={
                            adminData.runtimeConfig?.rateLimits.windowSeconds ??
                            0
                          }
                          onChangeSeconds={(value) =>
                            updateRuntimeRateLimitConfig({
                              windowSeconds: value,
                            })
                          }
                          onUnitChange={(unit) =>
                            setAdminTimeUnit("rate.windowSeconds", unit)
                          }
                        />
                        <AdminNumberField
                          label={t("emailVerificationLimit")}
                          min={1}
                          unitLabel={t("unitRequests")}
                          value={
                            adminData.runtimeConfig?.rateLimits
                              .emailVerificationLimit ?? 0
                          }
                          onChange={(value) =>
                            updateRuntimeRateLimitConfig({
                              emailVerificationLimit: value,
                            })
                          }
                        />
                        <AdminNumberField
                          label={t("registerLimit")}
                          min={1}
                          unitLabel={t("unitRequests")}
                          value={
                            adminData.runtimeConfig?.rateLimits.registerLimit ??
                            0
                          }
                          onChange={(value) =>
                            updateRuntimeRateLimitConfig({
                              registerLimit: value,
                            })
                          }
                        />
                        <AdminNumberField
                          label={t("loginLimit")}
                          min={1}
                          unitLabel={t("unitRequests")}
                          value={
                            adminData.runtimeConfig?.rateLimits.loginLimit ?? 0
                          }
                          onChange={(value) =>
                            updateRuntimeRateLimitConfig({
                              loginLimit: value,
                            })
                          }
                        />
                        <AdminNumberField
                          label={t("writeLimit")}
                          min={1}
                          unitLabel={t("unitRequests")}
                          value={
                            adminData.runtimeConfig?.rateLimits.writeLimit ?? 0
                          }
                          onChange={(value) =>
                            updateRuntimeRateLimitConfig({
                              writeLimit: value,
                            })
                          }
                        />
                        <AdminNumberField
                          label={t("uploadLimit")}
                          min={1}
                          unitLabel={t("unitRequests")}
                          value={
                            adminData.runtimeConfig?.rateLimits.uploadLimit ?? 0
                          }
                          onChange={(value) =>
                            updateRuntimeRateLimitConfig({
                              uploadLimit: value,
                            })
                          }
                        />
                        <AdminNumberField
                          label={t("shareCreateLimit")}
                          min={1}
                          unitLabel={t("unitRequests")}
                          value={
                            adminData.runtimeConfig?.rateLimits
                              .shareCreateLimit ?? 0
                          }
                          onChange={(value) =>
                            updateRuntimeRateLimitConfig({
                              shareCreateLimit: value,
                            })
                          }
                        />
                        <AdminNumberField
                          label={t("shareAccessLimit")}
                          min={1}
                          unitLabel={t("unitRequests")}
                          value={
                            adminData.runtimeConfig?.rateLimits
                              .shareAccessLimit ?? 0
                          }
                          onChange={(value) =>
                            updateRuntimeRateLimitConfig({
                              shareAccessLimit: value,
                            })
                          }
                        />
                        <AdminNumberField
                          label={t("downloadLimit")}
                          min={1}
                          unitLabel={t("unitRequests")}
                          value={
                            adminData.runtimeConfig?.rateLimits.downloadLimit ??
                            0
                          }
                          onChange={(value) =>
                            updateRuntimeRateLimitConfig({
                              downloadLimit: value,
                            })
                          }
                        />
                        <AdminNumberField
                          label={t("webhookLimit")}
                          min={1}
                          unitLabel={t("unitRequests")}
                          value={
                            adminData.runtimeConfig?.rateLimits.webhookLimit ??
                            0
                          }
                          onChange={(value) =>
                            updateRuntimeRateLimitConfig({
                              webhookLimit: value,
                            })
                          }
                        />
                      </div>
                    </article>
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => void saveRuntimeConfig()}
                    >
                      <ShieldCheck size={16} aria-hidden="true" />
                      {t("saveRuntimeConfig")}
                    </button>
                    <SaveFeedback result={saveResults[`runtime-${adminTab}`]} />
                  </section>
                ) : null}
                {adminTab === "overview" || adminTab === "services" ? (
                  <section className="admin-section admin-section--tiles">
                    <h3>{t("resourcePanel")}</h3>
                    <article className="list-card">
                      <strong>{t("cpu")}</strong>
                      <span>
                        {adminData.runtimePanel?.resources.cpuPercent ?? 0}%
                      </span>
                    </article>
                    <article className="list-card">
                      <strong>{t("memory")}</strong>
                      <span>
                        {formatBytes(
                          adminData.runtimePanel?.resources.memoryUsedBytes ??
                            0,
                        )}{" "}
                        /{" "}
                        {formatBytes(
                          adminData.runtimePanel?.resources.memoryTotalBytes ??
                            0,
                        )}{" "}
                        · {adminData.runtimePanel?.resources.memoryPercent ?? 0}
                        %
                      </span>
                    </article>
                    <article className="list-card">
                      <strong>{t("disk")}</strong>
                      <span>
                        {formatBytes(
                          adminData.runtimePanel?.resources.diskUsedBytes ?? 0,
                        )}{" "}
                        /{" "}
                        {formatBytes(
                          adminData.runtimePanel?.resources.diskTotalBytes ?? 0,
                        )}{" "}
                        · {adminData.runtimePanel?.resources.diskPercent ?? 0}%
                      </span>
                    </article>
                    <article className="list-card">
                      <strong>{t("objectStorage")}</strong>
                      <span>
                        {formatBytes(
                          adminData.runtimePanel?.resources
                            .objectStorageBytes ?? 0,
                        )}{" "}
                        ·{" "}
                        {adminData.runtimePanel?.resources
                          .objectStorageObjectCount ?? 0}{" "}
                        {t("files")}
                      </span>
                    </article>
                  </section>
                ) : null}
                {adminTab === "overview" || adminTab === "services" ? (
                  <section className="admin-section admin-section--tiles">
                    <h3>{t("providerStatus")}</h3>
                    {Object.entries(
                      adminData.runtimeConfig?.providerStatus ?? {},
                    ).map(([provider, status]) => (
                      <article className="list-card" key={provider}>
                        <div>
                          <strong>{providerLabel(provider, t)}</strong>
                          <span>
                            {status.configured
                              ? t("configurationReady")
                              : t("configurationIncomplete")}
                            {status.configured
                              ? ` · ${providerLabel(status.provider || provider, t)}`
                              : ` · ${t("missingConfiguration")}: ${(
                                  status.missingEnv ?? []
                                )
                                  .map((field) => providerFieldLabel(field, t))
                                  .join(", ")}`}
                          </span>
                          {status.lastTestStatus ? (
                            <span>
                              {providerTestStatusLabel(
                                status.lastTestStatus,
                                t,
                              )}
                            </span>
                          ) : null}
                        </div>
                        <button
                          type="button"
                          onClick={() => void adminProviderTest(provider)}
                        >
                          <CheckCircle2 size={16} aria-hidden="true" />
                          {t("verify")}
                        </button>
                      </article>
                    ))}
                  </section>
                ) : null}
                {adminTab === "plans" ? (
                  <section className="admin-section admin-section--wide">
                    <h3>{t("planCatalog")}</h3>
                    <div
                      className="catalog-tabs"
                      role="tablist"
                      aria-label={t("planCatalog")}
                    >
                      {(["plans", "prices", "redemptions"] as const).map(
                        (section) => (
                          <button
                            type="button"
                            role="tab"
                            key={section}
                            aria-selected={catalogSection === section}
                            className={
                              catalogSection === section ? "active" : ""
                            }
                            onClick={() => setCatalogSection(section)}
                          >
                            {t(
                              section === "plans"
                                ? "planLimits"
                                : section === "prices"
                                  ? "priceSettings"
                                  : "redemptionCodes",
                            )}
                          </button>
                        ),
                      )}
                    </div>
                    {catalogSection === "plans" ? (
                      <label className="field-row config-section-select">
                        <span>{t("choosePlan")}</span>
                        <select
                          value={catalogPlanId}
                          onChange={(event) =>
                            setCatalogPlanId(event.target.value)
                          }
                        >
                          {(catalog?.plans ?? []).map((plan) => (
                            <option key={plan.id} value={plan.id}>
                              {plan.name}
                            </option>
                          ))}
                        </select>
                      </label>
                    ) : null}
                    {catalogSection === "plans"
                      ? (catalog?.plans ?? [])
                          .filter((plan) => plan.id === catalogPlanId)
                          .map((plan) => (
                            <article className="list-card" key={plan.id}>
                              <strong>{plan.id}</strong>
                              <div className="form-grid">
                                <label className="field-row">
                                  <span>{t("title")}</span>
                                  <input
                                    value={plan.name}
                                    onChange={(event) =>
                                      updateCatalogPlan(plan.id, {
                                        name: event.target.value,
                                      })
                                    }
                                  />
                                </label>
                                <AdminNumberField
                                  label={t("activePasteLimitShort")}
                                  min={0}
                                  unitLabel={t("unitItems")}
                                  value={plan.activePasteLimit}
                                  onChange={(value) =>
                                    updateCatalogPlan(plan.id, {
                                      activePasteLimit: value,
                                    })
                                  }
                                />
                                <AdminSizeField
                                  label={t("activeStorageLimit")}
                                  minBytes={0}
                                  t={t}
                                  unit={adminSizeUnitFor(
                                    `plan.${plan.id}.activeStorageBytes`,
                                    plan.activeStorageBytes,
                                  )}
                                  valueBytes={plan.activeStorageBytes}
                                  onChangeBytes={(value) =>
                                    updateCatalogPlan(plan.id, {
                                      activeStorageBytes: value,
                                    })
                                  }
                                  onUnitChange={(unit) =>
                                    setAdminSizeUnit(
                                      `plan.${plan.id}.activeStorageBytes`,
                                      unit,
                                    )
                                  }
                                />
                                <AdminSizeField
                                  label={t("singleTextLimit")}
                                  minBytes={0}
                                  t={t}
                                  unit={adminSizeUnitFor(
                                    `plan.${plan.id}.singleTextBytes`,
                                    plan.singleTextBytes,
                                  )}
                                  valueBytes={plan.singleTextBytes}
                                  onChangeBytes={(value) =>
                                    updateCatalogPlan(plan.id, {
                                      singleTextBytes: value,
                                    })
                                  }
                                  onUnitChange={(unit) =>
                                    setAdminSizeUnit(
                                      `plan.${plan.id}.singleTextBytes`,
                                      unit,
                                    )
                                  }
                                />
                                <AdminSizeField
                                  label={t("singleFileLimit")}
                                  minBytes={0}
                                  t={t}
                                  unit={adminSizeUnitFor(
                                    `plan.${plan.id}.singleFileBytes`,
                                    plan.singleFileBytes,
                                  )}
                                  valueBytes={plan.singleFileBytes}
                                  onChangeBytes={(value) =>
                                    updateCatalogPlan(plan.id, {
                                      singleFileBytes: value,
                                    })
                                  }
                                  onUnitChange={(unit) =>
                                    setAdminSizeUnit(
                                      `plan.${plan.id}.singleFileBytes`,
                                      unit,
                                    )
                                  }
                                />
                                <AdminSizeField
                                  label={t("singlePasteLimit")}
                                  minBytes={0}
                                  t={t}
                                  unit={adminSizeUnitFor(
                                    `plan.${plan.id}.singlePasteBytes`,
                                    plan.singlePasteBytes,
                                  )}
                                  valueBytes={plan.singlePasteBytes}
                                  onChangeBytes={(value) =>
                                    updateCatalogPlan(plan.id, {
                                      singlePasteBytes: value,
                                    })
                                  }
                                  onUnitChange={(unit) =>
                                    setAdminSizeUnit(
                                      `plan.${plan.id}.singlePasteBytes`,
                                      unit,
                                    )
                                  }
                                />
                                <AdminNumberField
                                  label={t("attachmentsPerPaste")}
                                  min={0}
                                  unitLabel={t("unitFiles")}
                                  value={plan.attachmentsPerPasteLimit}
                                  onChange={(value) =>
                                    updateCatalogPlan(plan.id, {
                                      attachmentsPerPasteLimit: value,
                                    })
                                  }
                                />
                                <AdminNumberField
                                  label={t("tagsPerPaste")}
                                  min={0}
                                  unitLabel={t("tags")}
                                  value={plan.tagsPerPasteLimit}
                                  onChange={(value) =>
                                    updateCatalogPlan(plan.id, {
                                      tagsPerPasteLimit: value,
                                    })
                                  }
                                />
                                <AdminTimeField
                                  label={t("retentionSeconds")}
                                  minSeconds={1}
                                  t={t}
                                  unit={adminTimeUnitFor(
                                    `plan.${plan.id}.maxRetentionSeconds`,
                                    plan.maxRetentionSeconds,
                                  )}
                                  valueSeconds={plan.maxRetentionSeconds}
                                  onChangeSeconds={(value) =>
                                    updateCatalogPlan(plan.id, {
                                      maxRetentionSeconds: value,
                                    })
                                  }
                                  onUnitChange={(unit) =>
                                    setAdminTimeUnit(
                                      `plan.${plan.id}.maxRetentionSeconds`,
                                      unit,
                                    )
                                  }
                                />
                                <AdminSizeField
                                  label={t("dailyUploadLimit")}
                                  minBytes={0}
                                  t={t}
                                  unit={adminSizeUnitFor(
                                    `plan.${plan.id}.dailyUploadBytes`,
                                    plan.dailyUploadBytes,
                                  )}
                                  valueBytes={plan.dailyUploadBytes}
                                  onChangeBytes={(value) =>
                                    updateCatalogPlan(plan.id, {
                                      dailyUploadBytes: value,
                                    })
                                  }
                                  onUnitChange={(unit) =>
                                    setAdminSizeUnit(
                                      `plan.${plan.id}.dailyUploadBytes`,
                                      unit,
                                    )
                                  }
                                />
                                <AdminSizeField
                                  label={t("dailyShareDownloadLimit")}
                                  minBytes={0}
                                  t={t}
                                  unit={adminSizeUnitFor(
                                    `plan.${plan.id}.dailyShareDownloadBytes`,
                                    plan.dailyShareDownloadBytes,
                                  )}
                                  valueBytes={plan.dailyShareDownloadBytes}
                                  onChangeBytes={(value) =>
                                    updateCatalogPlan(plan.id, {
                                      dailyShareDownloadBytes: value,
                                    })
                                  }
                                  onUnitChange={(unit) =>
                                    setAdminSizeUnit(
                                      `plan.${plan.id}.dailyShareDownloadBytes`,
                                      unit,
                                    )
                                  }
                                />
                              </div>
                              <button
                                type="button"
                                disabled={busy}
                                onClick={() =>
                                  void saveAdminCatalog("plans", plan.id)
                                }
                              >
                                <ShieldCheck size={16} aria-hidden="true" />
                                {t("savePlan")}
                              </button>
                              <SaveFeedback
                                result={saveResults[`plans-${plan.id}`]}
                              />
                            </article>
                          ))
                      : null}
                    {catalogSection === "prices" ? (
                      <label className="field-row config-section-select">
                        <span>{t("choosePrice")}</span>
                        <select
                          value={catalogPriceId || catalog?.prices[0]?.id || ""}
                          onChange={(event) =>
                            setCatalogPriceId(event.target.value)
                          }
                        >
                          {(catalog?.prices ?? []).map((price) => (
                            <option key={price.id} value={price.id}>
                              {price.planId} ·{" "}
                              {t(
                                price.period === "monthly"
                                  ? "periodMonthly"
                                  : price.period === "yearly"
                                    ? "periodYearly"
                                    : price.period,
                              )}
                            </option>
                          ))}
                        </select>
                      </label>
                    ) : null}
                    {catalogSection === "prices"
                      ? (catalog?.prices ?? [])
                          .filter(
                            (price) =>
                              price.id ===
                              (catalogPriceId || catalog?.prices[0]?.id),
                          )
                          .map((price) => (
                            <article className="list-card" key={price.id}>
                              <strong>
                                {price.planId} ·{" "}
                                {price.period === "monthly"
                                  ? t("periodMonthly")
                                  : price.period === "yearly"
                                    ? t("periodYearly")
                                    : price.period}
                              </strong>
                              <div className="form-grid">
                                <label className="field-row">
                                  <span>{t("period")}</span>
                                  <input
                                    value={price.period}
                                    onChange={(event) =>
                                      updateCatalogPrice(price.id, {
                                        period: event.target.value,
                                      })
                                    }
                                  />
                                </label>
                                <label className="field-row">
                                  <span>{t("priceCents")}</span>
                                  <input
                                    min={0}
                                    type="number"
                                    value={price.amountCents}
                                    onChange={(event) =>
                                      updateCatalogPrice(price.id, {
                                        amountCents: Number(event.target.value),
                                      })
                                    }
                                  />
                                </label>
                                <label className="field-row">
                                  <span>{t("currency")}</span>
                                  <input
                                    maxLength={8}
                                    value={price.currency}
                                    onChange={(event) =>
                                      updateCatalogPrice(price.id, {
                                        currency: event.target.value,
                                      })
                                    }
                                  />
                                </label>
                                <label className="check-row">
                                  <input
                                    checked={price.visible}
                                    type="checkbox"
                                    onChange={(event) =>
                                      updateCatalogPrice(price.id, {
                                        visible: event.target.checked,
                                      })
                                    }
                                  />
                                  {t("visible")}
                                </label>
                                <label className="check-row">
                                  <input
                                    checked={price.purchaseEnabled}
                                    type="checkbox"
                                    onChange={(event) =>
                                      updateCatalogPrice(price.id, {
                                        purchaseEnabled: event.target.checked,
                                      })
                                    }
                                  />
                                  {t("purchase")}
                                </label>
                              </div>
                              <button
                                type="button"
                                disabled={busy}
                                onClick={() =>
                                  void saveAdminCatalog("prices", price.id)
                                }
                              >
                                <ShieldCheck size={16} aria-hidden="true" />
                                {t("savePrice")}
                              </button>
                              <SaveFeedback
                                result={saveResults[`prices-${price.id}`]}
                              />
                            </article>
                          ))
                      : null}
                  </section>
                ) : null}
                {adminTab === "plans" && catalogSection === "redemptions" ? (
                  <section className="admin-section admin-section--wide">
                    <h3>{t("redemptionCodes")}</h3>
                    <div className="form-grid">
                      <select
                        aria-label={t("currentPlan")}
                        value={redemptionDraft.planId}
                        onChange={(event) =>
                          setRedemptionDraft((previous) => ({
                            ...previous,
                            planId: event.target.value,
                          }))
                        }
                      >
                        {(catalog?.plans ?? []).map((plan) => (
                          <option key={plan.id} value={plan.id}>
                            {plan.name}
                          </option>
                        ))}
                      </select>
                      <input
                        aria-label={t("durationDays")}
                        min={1}
                        type="number"
                        value={redemptionDraft.durationDays}
                        onChange={(event) =>
                          setRedemptionDraft((previous) => ({
                            ...previous,
                            durationDays: Number(event.target.value),
                          }))
                        }
                      />
                      <input
                        aria-label={t("quantity")}
                        min={1}
                        type="number"
                        value={redemptionDraft.quantity}
                        onChange={(event) =>
                          setRedemptionDraft((previous) => ({
                            ...previous,
                            quantity: Number(event.target.value),
                          }))
                        }
                      />
                      <input
                        aria-label={t("note")}
                        maxLength={200}
                        placeholder={t("note")}
                        value={redemptionDraft.note}
                        onChange={(event) =>
                          setRedemptionDraft((previous) => ({
                            ...previous,
                            note: event.target.value,
                          }))
                        }
                      />
                    </div>
                    <button
                      type="button"
                      onClick={() => void createRedemptionBatch()}
                    >
                      <Archive size={16} aria-hidden="true" />
                      {t("createRedemptionBatch")}
                    </button>
                    <SaveFeedback result={saveResults["redemption-create"]} />
                    {adminData.redemptionBatches.slice(0, 5).map((batch) => (
                      <article className="list-card" key={batch.id}>
                        <div>
                          <strong>
                            {batch.planId} · {batch.quantity}
                          </strong>
                          <span>
                            {batch.redeemedCount}/{batch.maxTotalRedemptions} ·{" "}
                            {batch.disabled ? t("disabled") : t("enabled")}
                          </span>
                          {batch.codes?.[0]?.code ? (
                            <code>
                              {batch.codes.map((code) => code.code).join(", ")}
                            </code>
                          ) : null}
                        </div>
                        <button
                          type="button"
                          onClick={() => void toggleRedemptionBatch(batch)}
                        >
                          {batch.disabled ? (
                            <Undo2 size={16} aria-hidden="true" />
                          ) : (
                            <Ban size={16} aria-hidden="true" />
                          )}
                          {batch.disabled ? t("enabled") : t("disabled")}
                        </button>
                        <SaveFeedback
                          result={saveResults[`redemption-${batch.id}`]}
                        />
                      </article>
                    ))}
                  </section>
                ) : null}
                {adminTab === "overview" ? (
                  <section className="admin-section admin-section--tiles">
                    <h3>{t("manualReview")}</h3>
                    {adminData.manualWorkItems.length === 0 ? (
                      <article className="list-card">
                        <span>{t("noManualWorkItems")}</span>
                      </article>
                    ) : null}
                    {adminData.manualWorkItems.slice(0, 6).map((item) => (
                      <article
                        className="list-card"
                        key={`${item.kind}-${item.id}`}
                      >
                        <strong>{manualWorkKindLabel(item.kind, t)}</strong>
                        <span>
                          {adminStatusLabel(item.status, t)} · {item.summary}
                        </span>
                        {item.risk ? <span>{item.risk}</span> : null}
                      </article>
                    ))}
                  </section>
                ) : null}
                {adminTab === "services" ? (
                  <section className="admin-section admin-section--tiles">
                    <h3>{t("alertHistory")}</h3>
                    <div className="form-grid">
                      <input
                        aria-label={t("sendTestAlert")}
                        maxLength={240}
                        value={alertTestMessage}
                        onChange={(event) =>
                          setAlertTestMessage(event.target.value)
                        }
                      />
                      <button
                        type="button"
                        onClick={() => void sendAdminTestAlert()}
                      >
                        <Send size={16} aria-hidden="true" />
                        {t("sendTestAlert")}
                      </button>
                    </div>
                    {adminData.alerts.length === 0 ? (
                      <article className="list-card">
                        <span>{t("noRecentAlerts")}</span>
                      </article>
                    ) : null}
                    {adminData.alerts.slice(0, 5).map((alert) => (
                      <article className="list-card" key={alert.id}>
                        <strong>{adminStatusLabel(alert.status, t)}</strong>
                        <span>{runtimeAlertMessage(alert, t)}</span>
                        {alert.lastError ? (
                          <span>{alert.lastError}</span>
                        ) : null}
                      </article>
                    ))}
                  </section>
                ) : null}
                {adminTab === "overview" ? (
                  <section className="admin-section admin-section--tiles">
                    <h3>{t("users")}</h3>
                    {adminData.users.slice(0, 5).map((item) => (
                      <article className="list-card" key={item.id}>
                        <strong>{item.email}</strong>
                        <span>
                          {item.planId} ·{" "}
                          {item.frozen ? t("frozen") : t("active")}
                        </span>
                      </article>
                    ))}
                  </section>
                ) : null}
                {adminTab === "overview" ? (
                  <section className="admin-section">
                    <h3>{t("attachments")}</h3>
                    {adminData.attachments.slice(0, 5).map((attachment) => {
                      const scan = attachmentScanDetail(
                        attachment,
                        locale,
                        "owner",
                      );
                      return (
                        <article className="list-card" key={attachment.id}>
                          <div>
                            <strong>{attachment.fileName}</strong>
                            <span>
                              {scan.label} ·{" "}
                              {adminStatusLabel(attachment.status, t)} ·{" "}
                              {attachment.sha256.slice(0, 12)}
                            </span>
                          </div>
                          <div className="button-row compact">
                            <button
                              type="button"
                              onClick={() => void adminRetryScan(attachment.id)}
                            >
                              <RotateCcw size={16} aria-hidden="true" />
                              {t("retry")}
                            </button>
                            <button
                              type="button"
                              onClick={() =>
                                void adminFreezeAttachment(
                                  attachment.id,
                                  attachment.status !== "frozen",
                                )
                              }
                            >
                              {attachment.status === "frozen" ? (
                                <Undo2 size={16} aria-hidden="true" />
                              ) : (
                                <Snowflake size={16} aria-hidden="true" />
                              )}
                              {attachment.status === "frozen"
                                ? t("release")
                                : t("freeze")}
                            </button>
                          </div>
                        </article>
                      );
                    })}
                  </section>
                ) : null}
                {adminTab === "overview" ? (
                  <section className="admin-section">
                    <h3>{t("shares")}</h3>
                    {adminData.shares.slice(0, 5).map((share) => (
                      <article className="list-card" key={share.id}>
                        <div>
                          <strong>{share.id}</strong>
                          <span>
                            {share.visitCount} {t("visits")} ·{" "}
                            {share.downloadCount} {t("downloads")}
                          </span>
                        </div>
                        <button
                          type="button"
                          onClick={() => void adminRevokeShare(share.id)}
                          disabled={Boolean(share.revokedAt)}
                        >
                          <Trash2 size={16} aria-hidden="true" />
                          {t("revoke")}
                        </button>
                      </article>
                    ))}
                  </section>
                ) : null}
                {adminTab === "plans" ? (
                  <section className="admin-section">
                    <h3>{t("orders")}</h3>
                    {adminData.orders.slice(0, 5).map((order) => {
                      const status = orderStatusDetail(order.status, locale);
                      return (
                        <article className="list-card" key={order.id}>
                          <div>
                            <strong>{order.planId}</strong>
                            <span>
                              {providerLabel(order.provider, t)} ·{" "}
                              {(order.amountCents / 100).toFixed(2)}{" "}
                              {order.currency}
                            </span>
                            <span className="order-status-note">
                              {status.description}
                            </span>
                          </div>
                          <span
                            className={`order-status order-status--${status.tone}`}
                          >
                            {status.label}
                          </span>
                          <input
                            aria-label={t("manualPaymentReason")}
                            disabled={order.status === "paid"}
                            maxLength={500}
                            placeholder={t("manualPaymentReasonPlaceholder")}
                            value={adminPaymentReasons[order.id] ?? ""}
                            onChange={(event) =>
                              setAdminPaymentReasons((previous) => ({
                                ...previous,
                                [order.id]: event.target.value,
                              }))
                            }
                          />
                          <button
                            type="button"
                            onClick={() => void adminMarkOrderPaid(order.id)}
                            disabled={
                              order.status === "paid" ||
                              !(adminPaymentReasons[order.id] ?? "").trim()
                            }
                          >
                            <CreditCard size={16} aria-hidden="true" />
                            {t("paid")}
                          </button>
                        </article>
                      );
                    })}
                  </section>
                ) : null}
                {adminTab === "queues" ? (
                  <section className="admin-section admin-section--wide">
                    <h3>{t("queues")}</h3>
                    <article className="list-card">
                      <strong>{t("scanFailures")}</strong>
                      <span>{adminData.queues?.scanFailures.length ?? 0}</span>
                    </article>
                    <article className="list-card">
                      <strong>{t("cleanupJobs")}</strong>
                      <span>{adminData.queues?.cleanupJobs.length ?? 0}</span>
                    </article>
                    <article className="list-card">
                      <strong>{t("cleanupFailures")}</strong>
                      <span>
                        {adminData.queues?.cleanupFailures.length ?? 0}
                      </span>
                    </article>
                    <article className="list-card">
                      <strong>{t("failedJobs")}</strong>
                      <span>{adminData.queues?.failedJobs.length ?? 0}</span>
                    </article>
                    <article className="list-card">
                      <strong>{t("queuedMails")}</strong>
                      <span>{adminData.queues?.queuedMails.length ?? 0}</span>
                    </article>
                    <article className="list-card">
                      <strong>{t("failedMails")}</strong>
                      <span>{adminData.queues?.failedMails.length ?? 0}</span>
                    </article>
                    {(adminData.queues?.failedMails ?? [])
                      .slice(0, 5)
                      .map((mail) => (
                        <article className="list-card" key={mail.id}>
                          <div>
                            <strong>{mail.subject}</strong>
                            <span>
                              {mail.to} · {adminStatusLabel(mail.status, t)} ·{" "}
                              {mail.attempts} {t("attempts")}
                            </span>
                            {mail.lastError ? (
                              <span>{mail.lastError}</span>
                            ) : null}
                          </div>
                        </article>
                      ))}
                    {(adminData.queues?.reports ?? [])
                      .slice(0, 5)
                      .map((report) => (
                        <article className="list-card" key={report.id}>
                          <div>
                            <strong>{report.target}</strong>
                            <span>
                              {adminStatusLabel(report.status, t)} ·{" "}
                              {report.reason}
                            </span>
                          </div>
                          <div className="button-row compact">
                            <button
                              type="button"
                              onClick={() =>
                                void adminResolveReport(report, "resolved")
                              }
                              disabled={report.status === "resolved"}
                            >
                              <CheckCircle2 size={16} aria-hidden="true" />
                              {t("resolve")}
                            </button>
                            <button
                              type="button"
                              onClick={() =>
                                void adminResolveReport(report, "dismissed")
                              }
                              disabled={report.status === "dismissed"}
                            >
                              <Ban size={16} aria-hidden="true" />
                              {t("dismiss")}
                            </button>
                          </div>
                        </article>
                      ))}
                  </section>
                ) : null}
                {adminTab === "queues" ? (
                  <section className="admin-section admin-section--compact">
                    <h3>{t("webhooks")}</h3>
                    {adminData.webhookEvents.slice(0, 5).map((event) => (
                      <article className="list-card" key={event.id}>
                        <div>
                          <strong>{event.eventType}</strong>
                          <span>
                            {providerLabel(event.provider, t)} ·{" "}
                            {event.targetId}
                          </span>
                        </div>
                        <button
                          type="button"
                          onClick={() => void adminReplayWebhook(event.id)}
                        >
                          <RotateCcw size={16} aria-hidden="true" />
                          {t("replay")}
                        </button>
                      </article>
                    ))}
                  </section>
                ) : null}
              </div>
              {adminTab === "queues" ? (
                <section className="admin-audit-section">
                  <h3>{t("auditQueuesCleanup")}</h3>
                  <div className="admin-audit-list">
                    {auditLogs.slice(0, 8).map((log) => (
                      <article className="list-card" key={log.id}>
                        <strong>{humanizeIdentifier(log.action)}</strong>
                        <span>
                          {log.target} ·{" "}
                          {new Date(log.createdAt).toLocaleString()}
                        </span>
                      </article>
                    ))}
                  </div>
                </section>
              ) : null}
            </div>
          </Panel>
        ) : null}
        <WorkspaceFooter locale={locale} />
      </section>
    </main>
  );
}

function PasteList({
  pastes,
  selectedId,
  onSelect,
  onCopy,
  onDelete,
  onExtend,
  onToggleFlag,
  onFilterTag,
  activeTag,
  locale,
}: {
  pastes: Paste[];
  selectedId: string;
  onSelect: (id: string) => void;
  onCopy: (text: string) => void;
  onDelete: (id: string) => void;
  onExtend: (paste: Paste, expiresInSeconds: number) => void;
  onToggleFlag: (paste: Paste, field: "pinned" | "favorite") => void;
  onFilterTag: (tag: string) => void;
  activeTag: string;
  locale: Locale;
}) {
  const t = copyFor(locale);
  return (
    <section className="paste-list">
      <div className="section-heading">
        <h2>{t("recentPastes")}</h2>
        <span>
          {pastes.length} {t("active")}
        </span>
      </div>
      {pastes.map((paste) => (
        <article
          className={`paste-card ${selectedId === paste.id ? "selected" : ""}`}
          key={paste.id}
        >
          <button
            className="paste-main"
            type="button"
            onClick={() => onSelect(paste.id)}
          >
            <h3>{paste.title || t("untitledPaste")}</h3>
            <p>
              {paste.textPreview ||
                `${paste.attachments.length} ${t("attachmentsCount")}`}
            </p>
            {paste.attachments.length > 0 ? (
              // One send is one record, so the card lists the files that send
              // grouped together instead of hiding them behind a count.
              <span className="paste-file-summary">
                <FileUp size={13} aria-hidden="true" />
                {pasteFileSummary(paste.attachments)}
              </span>
            ) : null}
            <span>
              {formatBytes(paste.sizeBytes)} ·{" "}
              {formatDuration(paste.secondsToLive)} · {paste.scanStatus}
            </span>
          </button>
          <div className="card-actions">
            <button
              className={`icon-button small ${paste.pinned ? "active" : ""}`}
              type="button"
              onClick={() => onToggleFlag(paste, "pinned")}
              aria-pressed={paste.pinned}
              title={paste.pinned ? t("unpinPaste") : t("pinPaste")}
              aria-label={paste.pinned ? t("unpinPaste") : t("pinPaste")}
            >
              <Pin size={17} aria-hidden="true" />
              <span>{paste.pinned ? t("unpinPaste") : t("pinPaste")}</span>
            </button>
            <button
              className={`icon-button small ${paste.favorite ? "active" : ""}`}
              type="button"
              onClick={() => onToggleFlag(paste, "favorite")}
              aria-pressed={paste.favorite}
              title={paste.favorite ? t("removeFavorite") : t("favoritePaste")}
              aria-label={
                paste.favorite ? t("removeFavorite") : t("favoritePaste")
              }
            >
              <Star size={17} aria-hidden="true" />
              <span>
                {paste.favorite ? t("removeFavorite") : t("favoritePaste")}
              </span>
            </button>
            <button
              className="icon-button small"
              type="button"
              onClick={() => onCopy(paste.text)}
              aria-label={t("copyText")}
              title={t("copyText")}
            >
              <ClipboardCopy size={17} aria-hidden="true" />
              <span>{t("copyText")}</span>
            </button>
            <button
              className="icon-button small"
              type="button"
              onClick={() => onExtend(paste, 7 * 24 * 60 * 60)}
              aria-label={t("extendPaste")}
              title={t("extendPaste")}
            >
              <TimerReset size={17} aria-hidden="true" />
              <span>{t("extendPaste")}</span>
            </button>
            <button
              className="icon-button small danger"
              type="button"
              onClick={() => onDelete(paste.id)}
              aria-label={t("deletePaste")}
              title={t("deletePaste")}
            >
              <Trash2 size={17} aria-hidden="true" />
              <span>{t("deletePaste")}</span>
            </button>
          </div>
          {paste.shareCount ? (
            <span className="share-chip">{t("shared")}</span>
          ) : null}
          {paste.tags.length ? (
            <div className="paste-tags" aria-label={t("tags")}>
              {paste.tags.map((tag) => (
                <button
                  className={`tag-chip ${activeTag === tag ? "active" : ""}`}
                  type="button"
                  key={tag}
                  aria-pressed={activeTag === tag}
                  onClick={() => onFilterTag(activeTag === tag ? "" : tag)}
                >
                  <Tags size={13} aria-hidden="true" />
                  <span>{tag}</span>
                </button>
              ))}
            </div>
          ) : null}
          {paste.attachments.map((attachment) => (
            <AttachmentDownloadItem
              attachment={attachment}
              context="owner"
              href={attachmentDownloadPath(attachment.id)}
              icon="file"
              key={attachment.id}
              locale={locale}
            />
          ))}
        </article>
      ))}
    </section>
  );
}

type GuestWorkbenchCopy = {
  hint: string;
  titlePlaceholder: string;
  textPlaceholder: string;
  textLimit: string;
  fileLimit: string;
  copyLink: string;
  copied: string;
  missingText: string;
  disabled: string;
  overText: string;
  modalEyebrow: string;
  modalTitle: string;
  cancel: string;
  login: string;
};

const fallbackGuestUploads: GuestUploadConfig = {
  enabled: true,
  requireTurnstile: false,
  retentionSeconds: 6 * 60 * 60,
  activePasteLimit: 5,
  activeStorageBytes: 50 * 1024 * 1024,
  singleTextBytes: 64 * 1024,
  singleFileBytes: 10 * 1024 * 1024,
  singlePasteBytes: 15 * 1024 * 1024,
  attachmentsPerPasteLimit: 3,
  dailyUploadBytes: 100 * 1024 * 1024,
  dailyShareDownloadBytes: 100 * 1024 * 1024,
  shareDownloadsEnabled: true,
};

const guestWorkbenchCopy: Record<Locale, GuestWorkbenchCopy> = {
  en: {
    hint: "Guest mode creates a temporary share link.",
    titlePlaceholder: "Optional title",
    textPlaceholder: "Paste text here",
    textLimit: "Guest text limit",
    fileLimit: "Guest file limit",
    copyLink: "Copy link",
    copied: "Link copied.",
    missingText: "Enter text before creating a share.",
    disabled: "Guest workspace is closed. Sign in to use PasteBox.",
    overText: "This text is over the guest limit. Sign in for larger text.",
    modalEyebrow: "Guest limit",
    modalTitle: "Sign in for the full workspace",
    cancel: "Cancel",
    login: "Go login",
  },
  "zh-CN": {
    hint: "游客模式会生成一个临时分享链接。",
    titlePlaceholder: "可选标题",
    textPlaceholder: "把要分享的文字粘贴到这里",
    textLimit: "游客文本上限",
    fileLimit: "游客文件上限",
    copyLink: "复制链接",
    copied: "链接已复制。",
    missingText: "先输入要分享的文字。",
    disabled: "游客工作台暂未开放，登录后可以使用 PasteBox。",
    overText: "这段文字超过游客上限，注册后可以使用更大容量。",
    modalEyebrow: "游客额度",
    modalTitle: "登录后使用完整工作台",
    cancel: "取消",
    login: "去登录",
  },
  "zh-TW": {
    hint: "訪客模式會產生一個臨時分享連結。",
    titlePlaceholder: "可選標題",
    textPlaceholder: "把要分享的文字貼到這裡",
    textLimit: "訪客文字上限",
    fileLimit: "訪客檔案上限",
    copyLink: "複製連結",
    copied: "連結已複製。",
    missingText: "先輸入要分享的文字。",
    disabled: "訪客工作台暫未開放，登入後可以使用 PasteBox。",
    overText: "這段文字超過訪客上限，註冊後可以使用更大容量。",
    modalEyebrow: "訪客額度",
    modalTitle: "登入後使用完整工作台",
    cancel: "取消",
    login: "去登入",
  },
  es: {
    hint: "El modo invitado crea un enlace temporal.",
    titlePlaceholder: "Título opcional",
    textPlaceholder: "Pega el texto aquí",
    textLimit: "Límite de texto invitado",
    fileLimit: "Límite de archivo invitado",
    copyLink: "Copiar enlace",
    copied: "Enlace copiado.",
    missingText: "Ingresa texto antes de compartir.",
    disabled: "El espacio invitado está cerrado. Inicia sesión para usarlo.",
    overText: "Este texto supera el límite invitado. Inicia sesión.",
    modalEyebrow: "Límite invitado",
    modalTitle: "Inicia sesión para el espacio completo",
    cancel: "Cancelar",
    login: "Ir a login",
  },
};

function byteSize(value: string): number {
  return new Blob([value]).size;
}

type TransferCopy = {
  sendFile: string;
  attachToSelected: string;
  modeFile: string;
  modeImage: string;
  modeText: string;
  createRecord: string;
  linkReady: string;
  validUntil: string;
  failed: string;
  cancelSend: string;
  canceled: string;
  pickupTitle: string;
  pickupHint: string;
  pickupPlaceholder: string;
  pickupOpen: string;
  pickupInvalid: string;
  pickupCodeLabel: string;
  copyCode: string;
  pickupCodeCopied: string;
  pickupRateLimited: string;
  dropFilesHint: string;
  dropImageHint: string;
  chooseFiles: string;
  chooseImage: string;
  imageTypeError: string;
  filesStaged: string;
  removeFile: string;
  sendFiles: string;
  sendText: string;
  sendingText: string;
  queued: string;
  uploading: string;
  uploaded: string;
  uploadFailed: string;
  publishing: string;
  partialFailed: string;
  retryFailed: string;
  retrySend: string;
  totalSize: string;
  newSend: string;
  manifestFrozen: string;
  sendLocked: string;
  overFileLimit: string;
  overTotalLimit: string;
  overCountLimit: string;
  settingsLabel: string;
  expiryLabel: string;
  passwordLabel: string;
  passwordPlaceholder: string;
  loginRequired: string;
};

const transferCopy: Record<Locale, TransferCopy> = {
  en: {
    sendFile: "Send a file",
    attachToSelected: "Attach to selected record",
    modeFile: "File",
    modeImage: "Image",
    modeText: "Text",
    createRecord: "Record only (no share)",
    linkReady: "Share link is ready.",
    validUntil: "Valid until",
    failed: "The file could not be sent.",
    cancelSend: "Cancel this send",
    canceled: "The send was canceled and its content will be cleaned up.",
    pickupTitle: "Get files",
    pickupHint: "Open a share link or type the 6-character code you were sent.",
    pickupPlaceholder: "Share link or 6-character code",
    pickupOpen: "Open",
    pickupInvalid: "That link or pickup code is invalid or has expired.",
    pickupCodeLabel: "Pickup code",
    copyCode: "Copy code",
    pickupCodeCopied: "Pickup code copied.",
    pickupRateLimited: "Too many pickup code attempts. Try again shortly.",
    dropFilesHint: "Drop files here or choose several at once",
    chooseFiles: "Choose files",
    chooseImage: "Choose image",
    imageTypeError: "Choose an image file.",
    dropImageHint: "Drop or paste one image",
    filesStaged: "{count} files ready to send",
    removeFile: "Remove",
    sendFiles: "Send {count} files",
    sendText: "Send text",
    sendingText: "Sending...",
    queued: "Waiting",
    uploading: "Uploading",
    uploaded: "Uploaded",
    uploadFailed: "Upload failed",
    publishing: "Creating the share link...",
    partialFailed: "Some files did not upload.",
    retryFailed: "Retry failed files",
    retrySend: "Retry this send",
    totalSize: "Total",
    newSend: "Send another",
    manifestFrozen: "This send has already started.",
    sendLocked: "This send is finished.",
    overFileLimit: "A file is over the per-file size limit.",
    overTotalLimit: "The chosen files are over the total size limit for one send.",
    overCountLimit: "Too many files for one send.",
    settingsLabel: "Send settings",
    expiryLabel: "Expiry",
    passwordLabel: "Share password",
    passwordPlaceholder: "Password (optional)",
    loginRequired: "Require sign-in",
  },
  "zh-CN": {
    sendFile: "发送文件",
    attachToSelected: "附加到当前记录",
    modeFile: "文件",
    modeImage: "图片",
    modeText: "文本",
    createRecord: "仅创建记录（不分享）",
    linkReady: "分享链接已生成。",
    validUntil: "有效期至",
    failed: "文件发送失败。",
    cancelSend: "取消这次发送",
    canceled: "已取消这次发送，内容会进入后台清理。",
    pickupTitle: "取文件",
    pickupHint: "打开分享链接，或输入收到的 6 位取件码。",
    pickupPlaceholder: "分享链接或 6 位取件码",
    pickupOpen: "打开",
    pickupInvalid: "链接或取件码无效，或已过期。",
    pickupCodeLabel: "取件码",
    copyCode: "复制取件码",
    pickupCodeCopied: "取件码已复制。",
    pickupRateLimited: "取件码尝试次数过多，请稍后再试。",
    dropFilesHint: "拖入文件，或一次选择多个文件",
    chooseFiles: "选择文件",
    chooseImage: "选择图片",
    imageTypeError: "请选择图片文件。",
    dropImageHint: "拖入或粘贴一张图片",
    filesStaged: "已选 {count} 个文件",
    removeFile: "移除",
    sendFiles: "发送 {count} 个文件",
    sendText: "发送文本",
    sendingText: "发送中…",
    queued: "等待上传",
    uploading: "上传中",
    uploaded: "已上传",
    uploadFailed: "上传失败",
    publishing: "正在生成分享链接…",
    partialFailed: "有文件未上传成功。",
    retryFailed: "重试失败项",
    retrySend: "重试这次发送",
    totalSize: "合计",
    newSend: "再发一次",
    manifestFrozen: "本次发送已开始。",
    sendLocked: "本次发送已完成。",
    overFileLimit: "有文件超过单个文件大小上限。",
    overTotalLimit: "所选文件合计超过一次发送的大小上限。",
    overCountLimit: "所选文件数量超过一次发送的上限。",
    settingsLabel: "发送设置",
    expiryLabel: "有效期",
    passwordLabel: "分享密码",
    passwordPlaceholder: "密码（可选）",
    loginRequired: "需要登录",
  },
  "zh-TW": {
    sendFile: "傳送檔案",
    attachToSelected: "附加到目前記錄",
    modeFile: "檔案",
    modeImage: "圖片",
    modeText: "文字",
    createRecord: "僅建立記錄（不分享）",
    linkReady: "分享連結已產生。",
    validUntil: "有效期至",
    failed: "檔案傳送失敗。",
    cancelSend: "取消這次傳送",
    canceled: "已取消這次傳送，內容會進入背景清理。",
    pickupTitle: "取檔案",
    pickupHint: "開啟分享連結，或輸入收到的 6 位取件碼。",
    pickupPlaceholder: "分享連結或 6 位取件碼",
    pickupOpen: "開啟",
    pickupInvalid: "連結或取件碼無效，或已過期。",
    pickupCodeLabel: "取件碼",
    copyCode: "複製取件碼",
    pickupCodeCopied: "取件碼已複製。",
    pickupRateLimited: "取件碼嘗試次數過多，請稍後再試。",
    dropFilesHint: "拖入檔案，或一次選擇多個檔案",
    chooseFiles: "選擇檔案",
    chooseImage: "選擇圖片",
    imageTypeError: "請選擇圖片檔案。",
    dropImageHint: "拖入或貼上一張圖片",
    filesStaged: "已選 {count} 個檔案",
    removeFile: "移除",
    sendFiles: "傳送 {count} 個檔案",
    sendText: "傳送文字",
    sendingText: "傳送中…",
    queued: "等待上傳",
    uploading: "上傳中",
    uploaded: "已上傳",
    uploadFailed: "上傳失敗",
    publishing: "正在產生分享連結…",
    partialFailed: "有檔案未上傳成功。",
    retryFailed: "重試失敗項",
    retrySend: "重試這次傳送",
    totalSize: "合計",
    newSend: "再傳一次",
    manifestFrozen: "本次傳送已開始。",
    sendLocked: "本次傳送已完成。",
    overFileLimit: "有檔案超過單一檔案大小上限。",
    overTotalLimit: "所選檔案合計超過一次傳送的大小上限。",
    overCountLimit: "所選檔案數量超過一次傳送的上限。",
    settingsLabel: "傳送設定",
    expiryLabel: "有效期",
    passwordLabel: "分享密碼",
    passwordPlaceholder: "密碼（可選）",
    loginRequired: "需要登入",
  },
  es: {
    sendFile: "Enviar archivo",
    attachToSelected: "Adjuntar al registro seleccionado",
    modeFile: "Archivo",
    modeImage: "Imagen",
    modeText: "Texto",
    createRecord: "Solo registro (sin enlace)",
    linkReady: "Enlace listo.",
    validUntil: "Válido hasta",
    failed: "No se pudo enviar el archivo.",
    cancelSend: "Cancelar este envío",
    canceled: "Envío cancelado; su contenido se limpiará en segundo plano.",
    pickupTitle: "Recibir archivos",
    pickupHint:
      "Abre un enlace o escribe el código de 6 caracteres que te enviaron.",
    pickupPlaceholder: "Enlace o código de 6 caracteres",
    pickupOpen: "Abrir",
    pickupInvalid: "El enlace o el código no es válido o ha caducado.",
    pickupCodeLabel: "Código de recogida",
    copyCode: "Copiar código",
    pickupCodeCopied: "Código copiado.",
    pickupRateLimited:
      "Demasiados intentos de código. Inténtalo en un momento.",
    dropFilesHint: "Arrastra archivos o elige varios a la vez",
    chooseFiles: "Elegir archivos",
    chooseImage: "Elegir imagen",
    imageTypeError: "Elige un archivo de imagen.",
    dropImageHint: "Arrastra o pega una imagen",
    filesStaged: "{count} archivos listos para enviar",
    removeFile: "Quitar",
    sendFiles: "Enviar {count} archivos",
    sendText: "Enviar texto",
    sendingText: "Enviando...",
    queued: "En espera",
    uploading: "Subiendo",
    uploaded: "Subido",
    uploadFailed: "Error al subir",
    publishing: "Creando el enlace...",
    partialFailed: "Algunos archivos no se subieron.",
    retryFailed: "Reintentar archivos fallidos",
    retrySend: "Reintentar este envío",
    totalSize: "Total",
    newSend: "Enviar otro",
    manifestFrozen: "Este envío ya empezó.",
    sendLocked: "Este envío ya terminó.",
    overFileLimit: "Un archivo supera el límite de tamaño por archivo.",
    overTotalLimit:
      "Los archivos elegidos superan el tamaño total de un envío.",
    overCountLimit: "Demasiados archivos para un envío.",
    settingsLabel: "Ajustes de envío",
    expiryLabel: "Caducidad",
    passwordLabel: "Contraseña del enlace",
    passwordPlaceholder: "Contraseña (opcional)",
    loginRequired: "Requerir sesión",
  },
};

function transferCopyFor(locale: Locale): TransferCopy {
  return transferCopy[locale] ?? transferCopy.en;
}

// fillCopy renders a copy template such as "Send {count} files" without
// splitting the sentence into fragments that other languages would reorder.
function fillCopy(
  template: string,
  values: Record<string, string | number>,
): string {
  return template.replace(/\{(\w+)\}/g, (match, key: string) =>
    key in values ? String(values[key]) : match,
  );
}

// sendQueuePercent is the real byte progress of one queued file, not a timer.
function sendQueuePercent(item: TransferQueueItem): number {
  if (item.status === "uploaded") return 100;
  if (item.total <= 0) return item.status === "uploading" ? 100 : 0;
  return Math.min(100, Math.round((item.loaded / item.total) * 100));
}

function sendQueueStateLabel(
  item: TransferQueueItem,
  labels: TransferCopy,
): string {
  switch (item.status) {
    case "uploading":
      return `${labels.uploading} ${sendQueuePercent(item)}%`;
    case "uploaded":
      return labels.uploaded;
    case "failed":
      return labels.uploadFailed;
    default:
      return labels.queued;
  }
}

// SendLimits are the per-send bounds of whichever plan or guest allowance
// applies to the sender.
type SendLimits = {
  singleFileBytes: number;
  singlePasteBytes: number;
  attachmentsPerPasteLimit: number;
};

type SendLimitViolation = "file" | "total" | "count";

// checkSendLimits reports which limit the chosen files would break, so both
// send areas refuse the same batch for the same reason.
function checkSendLimits(
  staged: TransferQueueItem[],
  files: File[],
  limits: SendLimits,
): SendLimitViolation | "" {
  if (files.length === 0) return "";
  if (
    limits.singleFileBytes > 0 &&
    files.some((file) => file.size > limits.singleFileBytes)
  ) {
    return "file";
  }
  const nextTotal =
    staged.reduce((sum, item) => sum + item.file.size, 0) +
    files.reduce((sum, file) => sum + file.size, 0);
  if (limits.singlePasteBytes > 0 && nextTotal > limits.singlePasteBytes) {
    return "total";
  }
  if (
    limits.attachmentsPerPasteLimit > 0 &&
    staged.length + files.length > limits.attachmentsPerPasteLimit
  ) {
    return "count";
  }
  return "";
}

function sendLimitCopy(
  violation: SendLimitViolation,
  labels: TransferCopy,
): string {
  switch (violation) {
    case "file":
      return labels.overFileLimit;
    case "total":
      return labels.overTotalLimit;
    default:
      return labels.overCountLimit;
  }
}

// pasteFileSummary names the files one send grouped, staying short for large
// sends so a record card cannot grow with the file count.
function pasteFileSummary(attachments: Attachment[]): string {
  const names = attachments
    .slice(0, 3)
    .map((attachment) => attachment.fileName);
  const hidden = attachments.length - names.length;
  return hidden > 0 ? `${names.join(" · ")} +${hidden}` : names.join(" · ");
}

// sendQueueStatus is the single line describing a send: how far it got, or why
// it stopped.
function sendQueueStatus(
  phase: TransferQueuePhase,
  counts: { uploaded: number; total: number; failed: number },
  error: string,
  labels: TransferCopy,
): string {
  switch (phase) {
    case "uploading":
      return `${labels.uploading} ${counts.uploaded}/${counts.total}`;
    case "publishing":
      return labels.publishing;
    case "failed":
      return counts.failed > 0 ? labels.partialFailed : error || labels.failed;
    default:
      return "";
  }
}

// A drop zone is inert once a send starts, so it says so instead of looking
// like it still accepts files.
function sendZoneLabel(
  phase: TransferQueuePhase,
  labels: TransferCopy,
): string {
  return phase === "published" ? labels.sendLocked : labels.manifestFrozen;
}

// ImageStageResult is what one image pick produced: the image was staged,
// there was nothing to stage, or the pick is refused with the reason to show.
type ImageStageResult =
  | { kind: "staged" }
  | { kind: "none" }
  | { kind: "error"; message: string }
  | { kind: "limit"; message: string };

// stageOneImage keeps image mode to a single image: a new pick replaces a draft
// that has not started and starts over after a finished send. A failed draft is
// canceled on the server first so a replacement does not leave it behind; a
// running upload has to be canceled by the user before the image can change.
// Both send areas share this so image mode behaves the same wherever it runs.
async function stageOneImage(
  queue: TransferQueue,
  file: File | undefined,
  limits: SendLimits,
  labels: TransferCopy,
): Promise<ImageStageResult> {
  if (!file) return { kind: "none" };
  if (!file.type.startsWith("image/")) {
    return { kind: "error", message: labels.imageTypeError };
  }
  if (queue.phase === "uploading" || queue.phase === "publishing") {
    return { kind: "error", message: labels.manifestFrozen };
  }
  if (queue.phase === "failed") {
    await queue.cancel();
  }
  const replacing = queue.items.length > 0;
  if (replacing) queue.reset();
  const staged = replacing ? [] : queue.items;
  const violation = checkSendLimits(staged, [file], limits);
  if (violation) {
    return { kind: "limit", message: sendLimitCopy(violation, labels) };
  }
  if (!queue.addFiles([file])) {
    return { kind: "error", message: labels.manifestFrozen };
  }
  return { kind: "staged" };
}

// SendMode is what one send carries. File is the default on both send areas
// because PasteBox leads with file transfer; text and image stay one switch
// away.
type SendMode = "file" | "image" | "text";

const sendModes: SendMode[] = ["file", "image", "text"];

function sendModeLabel(mode: SendMode, labels: TransferCopy): string {
  if (mode === "image") return labels.modeImage;
  if (mode === "text") return labels.modeText;
  return labels.modeFile;
}

function sendModeIcon(mode: SendMode): ReactNode {
  if (mode === "image") return <ImageIcon size={16} />;
  if (mode === "text") return <FileText size={16} />;
  return <FileUp size={16} />;
}

// SendModeTabs is the single mode switch both send areas use, so the guest
// workbench and the account composer offer the same three entries in the same
// order with the same keyboard behaviour.
function SendModeTabs({
  ariaLabel,
  className,
  idPrefix,
  mode,
  onSelect,
  labels,
}: {
  ariaLabel: string;
  className: string;
  idPrefix: string;
  mode: SendMode;
  onSelect: (mode: SendMode) => void;
  labels: TransferCopy;
}) {
  function handleKeyDown(
    event: KeyboardEvent<HTMLButtonElement>,
    index: number,
  ) {
    let nextIndex = index;
    if (event.key === "ArrowRight" || event.key === "ArrowDown") {
      nextIndex = (index + 1) % sendModes.length;
    } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
      nextIndex = (index - 1 + sendModes.length) % sendModes.length;
    } else if (event.key === "Home") {
      nextIndex = 0;
    } else if (event.key === "End") {
      nextIndex = sendModes.length - 1;
    } else {
      return;
    }
    event.preventDefault();
    const next = sendModes[nextIndex];
    onSelect(next);
    window.requestAnimationFrame(() => {
      document.getElementById(`${idPrefix}-tab-${next}`)?.focus();
    });
  }

  return (
    <div className={className} role="tablist" aria-label={ariaLabel}>
      {sendModes.map((item, index) => (
        <button
          aria-controls={`${idPrefix}-panel-${item}`}
          aria-selected={mode === item}
          className={mode === item ? "active" : ""}
          id={`${idPrefix}-tab-${item}`}
          key={item}
          onClick={() => onSelect(item)}
          onKeyDown={(event) => handleKeyDown(event, index)}
          role="tab"
          tabIndex={mode === item ? 0 : -1}
          type="button"
        >
          {sendModeIcon(item)}
          {sendModeLabel(item, labels)}
        </button>
      ))}
    </div>
  );
}

// SendShareResult is the success panel of one published send: the link, the
// 6-character pickup code and the expiry. Both send areas render it, so a text
// send and a file send hand over the same credentials in the same shape.
function SendShareResult({
  copyLabel,
  lineClassName,
  onCopy,
  onCopyCode,
  onReset,
  resetLabel,
  share,
  showReadyLine = true,
  wrapperClassName,
  labels,
}: {
  copyLabel: string;
  lineClassName: string;
  onCopy: (value: string) => void;
  onCopyCode?: (value: string) => void;
  onReset?: () => void;
  resetLabel?: string;
  share: TransferQueueShare;
  showReadyLine?: boolean;
  wrapperClassName: string;
  labels: TransferCopy;
}) {
  return (
    <div className={wrapperClassName}>
      {showReadyLine ? (
        <p className={lineClassName}>{labels.linkReady}</p>
      ) : null}
      <div className="guest-share-result">
        <input readOnly value={share.url} />
        <button type="button" onClick={() => onCopy(share.url)}>
          <ClipboardCopy size={16} aria-hidden="true" />
          {copyLabel}
        </button>
      </div>
      <PickupCodeRow
        code={share.pickupCode ?? ""}
        labelClassName={lineClassName}
        labels={labels}
        onCopy={onCopyCode ?? onCopy}
      />
      <p className={lineClassName}>
        {labels.validUntil} {new Date(share.expiresAt).toLocaleString()}
      </p>
      {onReset && resetLabel ? (
        <button type="button" onClick={onReset}>
          {resetLabel}
        </button>
      ) : null}
    </div>
  );
}

// textSendStatus is the one line a text send shows while it runs or when it
// stops, matching how a file send reports itself.
function textSendStatus(
  phase: TextSendPhase,
  error: string,
  labels: TransferCopy,
): string {
  if (phase === "sending") return labels.sendingText;
  if (phase === "failed") return error || labels.failed;
  return "";
}

// shareTokenFromShareLink accepts a full URL, a "/s/<token>" path or a bare
// token so the pickup entry can take whatever the sender copied.
function shareTokenFromShareLink(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return "";
  try {
    const url = new URL(trimmed, window.location.origin);
    const match = url.pathname.match(/^\/s\/([^/?#]+)/);
    if (match) return decodeURIComponent(match[1]);
  } catch {
    // Fall through to the path/token heuristics below.
  }
  const pathMatch = trimmed.match(/(?:^|\/)s\/([^/?#\s]+)/);
  if (pathMatch) return pathMatch[1];
  if (/^[A-Za-z0-9_-]{16,}$/.test(trimmed)) return trimmed;
  return "";
}

// A typed pickup code is 6 alphanumerics, with the separators people add by
// hand removed. The server owns the exact alphabet (it rejects characters that
// are easy to confuse) and the access rules, so this only screens out input
// that cannot be a code at all.
function pickupCodeFromInput(value: string): string {
  const code = value
    .trim()
    .replace(/[\s_-]+/g, "")
    .toUpperCase();
  return /^[A-Z0-9]{6}$/.test(code) ? code : "";
}

// The expiry ladder the send area offers. Choices above the sender's retention
// are never rendered, because the service would shorten them: the send form
// only promises lifetimes the configured policy actually supports.
const expiryChoices: Array<{ seconds: number; labelKey?: string }> = [
  { seconds: 60 * 60, labelKey: "duration1Hour" },
  { seconds: 6 * 60 * 60, labelKey: "duration6Hours" },
  { seconds: 24 * 60 * 60, labelKey: "duration24Hours" },
  { seconds: 7 * 24 * 60 * 60, labelKey: "duration7Days" },
  { seconds: 30 * 24 * 60 * 60, labelKey: "duration30Days" },
  { seconds: 180 * 24 * 60 * 60, labelKey: "duration180Days" },
];

// expiryOptionsFor returns the lifetimes a sender may choose under the given
// retention ceiling. A ceiling that is not on the ladder is added so the sender
// can always pick the policy maximum; an unknown ceiling (0, catalog not loaded
// yet) offers the current value only instead of inventing a limit.
function expiryOptionsFor(
  maxSeconds: number,
  currentSeconds: number,
  t: Translate,
): Array<{ seconds: number; label: string }> {
  if (maxSeconds <= 0) {
    return [{ seconds: currentSeconds, label: formatDuration(currentSeconds) }];
  }
  const allowed = expiryChoices.filter(
    (choice) => choice.seconds <= maxSeconds,
  );
  if (!allowed.some((choice) => choice.seconds === maxSeconds)) {
    allowed.push({ seconds: maxSeconds });
  }
  return allowed.map((choice) => ({
    seconds: choice.seconds,
    label: choice.labelKey
      ? t(choice.labelKey)
      : formatDuration(choice.seconds),
  }));
}

// clampExpirySeconds keeps a chosen lifetime inside the retention that applies
// to the sender, matching the service rule that a share never outlives the
// content it points at.
function clampExpirySeconds(seconds: number, maxSeconds: number): number {
  if (maxSeconds <= 0) return seconds;
  if (seconds <= 0 || seconds > maxSeconds) return maxSeconds;
  return seconds;
}

// PickupCodeRow shows the short code beside the share link on both success
// pages, so a sender can hand over 6 characters instead of a long URL. Each
// page supplies its own copy confirmation.
function PickupCodeRow({
  code,
  labelClassName,
  labels,
  onCopy,
}: {
  code: string;
  labelClassName: string;
  labels: TransferCopy;
  onCopy: (code: string) => void;
}) {
  if (!code) return null;
  return (
    <>
      <p className={labelClassName}>{labels.pickupCodeLabel}</p>
      <div className="guest-share-result">
        <input aria-label={labels.pickupCodeLabel} readOnly value={code} />
        <button type="button" onClick={() => onCopy(code)}>
          <ClipboardCopy size={16} aria-hidden="true" />
          {labels.copyCode}
        </button>
      </div>
    </>
  );
}

// SendSettingsFields is the inline expiry and privacy group shared by the
// account composer and the guest workbench: both send flows offer the same
// settings next to the files, so nobody has to leave the send flow to protect a
// transfer. The sign-in requirement is account-only, so it renders only when
// the surface can enforce it.
function SendSettingsFields({
  expiresInSeconds,
  expiryOptions,
  labels,
  loginRequired = false,
  onExpiry,
  onLoginRequired,
  onPassword,
  password,
}: {
  expiresInSeconds: number;
  expiryOptions: Array<{ seconds: number; label: string }>;
  labels: TransferCopy;
  loginRequired?: boolean;
  onExpiry: (seconds: number) => void;
  onLoginRequired?: (value: boolean) => void;
  onPassword: (value: string) => void;
  password: string;
}) {
  const labelId = useId();
  return (
    <div className="send-settings-group">
      {/* The group is named on screen, so a password typed here cannot be
          mistaken for a setting of the record the composer also creates. */}
      <span className="send-settings-label" id={labelId}>
        {labels.settingsLabel}
      </span>
      <div aria-labelledby={labelId} className="send-settings" role="group">
        <label>
          <Clock3 size={16} aria-hidden="true" />
          <select
            aria-label={labels.expiryLabel}
            disabled={expiryOptions.length < 2}
            value={expiresInSeconds}
            onChange={(event) => onExpiry(Number(event.target.value))}
          >
            {expiryOptions.map((option) => (
              <option key={option.seconds} value={option.seconds}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
        <label>
          <KeyRound size={16} aria-hidden="true" />
          <input
            aria-label={labels.passwordLabel}
            autoComplete="new-password"
            onChange={(event) => onPassword(event.target.value)}
            placeholder={labels.passwordPlaceholder}
            type="password"
            value={password}
          />
        </label>
        {onLoginRequired ? (
          <label className="send-settings-toggle">
            <input
              checked={loginRequired}
              onChange={(event) => onLoginRequired(event.target.checked)}
              type="checkbox"
            />
            <span>{labels.loginRequired}</span>
          </label>
        ) : null}
      </div>
    </div>
  );
}

// UsageBoundaryNotice states the content rules and points at the same abuse and
// privacy pages the footer links use, so the wording for content norms,
// reporting and privacy stays identical wherever it appears.
function UsageBoundaryNotice({ locale }: { locale: Locale }) {
  const t = copyFor(locale);
  return (
    <p className="usage-boundary">
      <span>{t("contentRules")}</span>
      <a href="/legal/abuse">{t("abuseDmca")}</a>
      <a href="/legal/privacy">{t("privacy")}</a>
    </p>
  );
}

function PickupEntry({ locale }: { locale: Locale }) {
  const labels = transferCopyFor(locale);
  const [value, setValue] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  // A pasted link already carries its token, so only a typed code needs a
  // lookup. Both end up on the same share page with the same password and
  // login rules.
  async function openEntry() {
    const token = shareTokenFromShareLink(value);
    if (token) {
      window.location.href = `/s/${encodeURIComponent(token)}`;
      return;
    }
    const code = pickupCodeFromInput(value);
    if (!code) {
      setMessage(labels.pickupInvalid);
      return;
    }
    setBusy(true);
    setMessage("");
    try {
      const resolved = await client.resolvePickupCode(code);
      window.location.href = `/s/${encodeURIComponent(resolved.token)}`;
    } catch (error) {
      const apiError = error as ApiError;
      setMessage(
        apiError.status === 429
          ? labels.pickupRateLimited
          : labels.pickupInvalid,
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      className="pickup-entry"
      onSubmit={(event) => {
        event.preventDefault();
        void openEntry();
      }}
    >
      <div className="pickup-entry-heading">
        <strong>{labels.pickupTitle}</strong>
        <span>{labels.pickupHint}</span>
      </div>
      <div className="pickup-entry-row">
        <input
          aria-label={labels.pickupPlaceholder}
          placeholder={labels.pickupPlaceholder}
          value={value}
          onChange={(event) => {
            setValue(event.target.value);
            setMessage("");
          }}
        />
        <button disabled={busy} type="submit">
          <Link2 size={16} aria-hidden="true" />
          {labels.pickupOpen}
        </button>
      </div>
      {message ? <p className="guest-status">{message}</p> : null}
    </form>
  );
}

// landingFacts states at most four real numbers from the published
// configuration: capacity per send, retention, per-file ceiling and the sign-in
// requirement. With no catalog there is nothing the server has promised, so the
// first screen states nothing rather than falling back to local numbers.
function landingFacts(
  catalog: PlanCatalog | null,
  t: Translate,
): Array<{ label: string; value: string }> {
  if (!catalog) return [];
  const guest = catalog.guestUploads;
  const freePlan =
    catalog.plans.find((plan) => plan.id === "free") ?? catalog.plans[0];
  // Describe what the visitor can do right now: the guest allowance while guest
  // sending is open, the free plan once it is not.
  const allowance =
    guest?.enabled || !freePlan
      ? {
          capacity: guest?.singlePasteBytes ?? 0,
          retention: guest?.retentionSeconds ?? 0,
          fileLimit: guest?.singleFileBytes ?? 0,
          signup: t("factSignupNotRequired"),
        }
      : {
          capacity: freePlan.singlePasteBytes,
          retention: freePlan.maxRetentionSeconds,
          fileLimit: freePlan.singleFileBytes,
          signup: t("factSignupRequired"),
        };
  const facts = [
    { label: t("factCapacity"), value: formatBytes(allowance.capacity) },
    { label: t("factRetention"), value: formatDuration(allowance.retention) },
    { label: t("factFileLimit"), value: formatBytes(allowance.fileLimit) },
    { label: t("factSignup"), value: allowance.signup },
  ];
  return facts.slice(0, 4);
}

function LandingPage({
  catalog,
  locale,
  user,
  sessionChecked,
}: {
  catalog: PlanCatalog | null;
  locale: Locale;
  user: User | null;
  sessionChecked: boolean;
}) {
  const t = copyFor(locale);
  const content = landingContentFor(locale);
  const plans = catalog?.plans ?? [];
  const visiblePaidPlanIds = new Set(
    (catalog?.prices ?? [])
      .filter(
        (price) =>
          price.visible && price.purchaseEnabled && price.planId !== "free",
      )
      .map((price) => price.planId),
  );
  const priceCards = plans.filter(
    (plan) => plan.id === "free" || visiblePaidPlanIds.has(plan.id),
  );
  const showPricing = visiblePaidPlanIds.size > 0 && priceCards.length > 0;
  const showcasePlan = priceCards[0];
  const guestUploads = catalog?.guestUploads ?? fallbackGuestUploads;
  const facts = landingFacts(catalog, t);

  return (
    <main className="landing-page">
      <header className="landing-nav">
        <a className="brand-mark landing-brand" href="/">
          <div className="brand-icon">
            <ClipboardCopy size={22} aria-hidden="true" />
          </div>
          <div>
            <strong>PasteBox</strong>
            <span>{t("privateCloudClipboard")}</span>
          </div>
        </a>
        <nav className="landing-links" aria-label="Product navigation">
          <a href="#product">{content.navProduct}</a>
          <a href="#security">{content.navSecurity}</a>
          {showPricing ? <a href="#pricing">{content.navPricing}</a> : null}
        </nav>
        <div className="landing-actions">
          {!sessionChecked ? (
            <span role="status">{t("loadingSession")}</span>
          ) : user ? (
            <a className="landing-primary-button landing-user" href="/app">
              <UserRound size={18} aria-hidden="true" />
              <span>{user.displayName || user.email}</span>
              <span>{t("openWorkspace")}</span>
            </a>
          ) : (
            <>
              <a
                className="landing-link-button"
                href={user ? "/app" : "/login"}
              >
                {t("login")}
              </a>
              <a
                className="landing-primary-button"
                href={user ? "/app" : "/register"}
              >
                {t("register")}
              </a>
            </>
          )}
        </div>
      </header>

      <section className="landing-hero" id="product">
        <div className="landing-copy">
          <span className="eyebrow">{content.eyebrow}</span>
          <h1>{content.title}</h1>
          <p>{content.subtitle}</p>
          <div className="landing-cta-row">
            <a
              className="landing-primary-button large"
              href={user ? "/app" : "/register"}
            >
              <Sparkles size={18} aria-hidden="true" />
              {user ? t("openWorkspace") : content.primaryCta}
            </a>
            <a
              className="landing-link-button large"
              href={user ? "/app" : "/login"}
            >
              <KeyRound size={18} aria-hidden="true" />
              {user ? user.displayName || user.email : content.secondaryCta}
            </a>
          </div>
          <PickupEntry locale={locale} />
          {facts.length > 0 ? (
            <ul className="landing-facts">
              {facts.map((fact) => (
                <li key={fact.label}>
                  <strong>{fact.value}</strong>
                  <span>{fact.label}</span>
                </li>
              ))}
            </ul>
          ) : null}
        </div>

        <div className="landing-hero-art">
          <img
            alt=""
            aria-hidden="true"
            className="clay-scene landing-clay-scene"
            src={clayHeroAsset}
          />
          <GuestWorkbench config={guestUploads} locale={locale} />
        </div>
      </section>

      <section className="landing-strip" aria-label="Clipboard workflow">
        {content.steps.map((step, index) => (
          <article key={step.title}>
            <span>{String(index + 1).padStart(2, "0")}</span>
            <strong>{step.title}</strong>
            <p>{step.body}</p>
          </article>
        ))}
      </section>

      <section className="landing-feature-grid" id="security">
        <div className="landing-section-heading">
          <span className="eyebrow">{content.workspaceTitle}</span>
          <h2>{content.workspaceBody}</h2>
        </div>
        {content.features.map((feature) => (
          <article className="landing-feature-card" key={feature.title}>
            <span>{feature.stat}</span>
            <strong>{feature.title}</strong>
            <p>{feature.body}</p>
          </article>
        ))}
      </section>

      {showPricing ? (
        <section className="landing-pricing" id="pricing">
          <div className="landing-section-heading">
            <span className="eyebrow">{content.navPricing}</span>
            <h2>{t("stripeUsdtPayments")}</h2>
          </div>
          <div className="landing-plan-grid">
            {priceCards.map((plan) => (
              <article
                className={`landing-plan-card landing-plan-card--${plan.id}`}
                key={plan.id}
              >
                <strong>{plan.name}</strong>
                <span>
                  {plan.activePasteLimit.toLocaleString()} {t("activePastes")}
                </span>
                <dl>
                  <div>
                    <dt>{t("storage")}</dt>
                    <dd>{formatBytes(plan.activeStorageBytes)}</dd>
                  </div>
                  <div>
                    <dt>{t("retention")}</dt>
                    <dd>{formatDuration(plan.maxRetentionSeconds)}</dd>
                  </div>
                </dl>
              </article>
            ))}
          </div>
          {showcasePlan ? (
            <p className="landing-footnote">
              {showcasePlan.name} ·{" "}
              {formatBytes(showcasePlan.activeStorageBytes)} ·{" "}
              {formatDuration(showcasePlan.maxRetentionSeconds)}
            </p>
          ) : null}
        </section>
      ) : null}

      <section className="landing-cta-band" aria-label={content.ctaTitle}>
        <div className="landing-cta-band-copy">
          <h2>{content.ctaTitle}</h2>
          <p>{content.ctaBody}</p>
          <ul className="landing-cta-badges">
            {content.ctaBadges.map((badge) => (
              <li key={badge}>
                <CheckCircle2 size={15} aria-hidden="true" />
                {badge}
              </li>
            ))}
          </ul>
        </div>
        <a
          className="landing-primary-button large"
          href={user ? "/app" : "/register"}
        >
          <Sparkles size={18} aria-hidden="true" />
          {user ? t("openWorkspace") : content.primaryCta}
        </a>
      </section>

      <PublicFooter locale={locale} />
    </main>
  );
}

// SendQueueList renders the files of one send with their real upload progress.
// Both the account composer and the guest workbench share it so a queue looks
// and behaves the same wherever a file is sent from.
function SendQueueList({
  items,
  labels,
  canRemove,
  onRemove,
}: {
  items: TransferQueueItem[];
  labels: TransferCopy;
  canRemove: boolean;
  onRemove: (id: string) => void;
}) {
  if (items.length === 0) return null;
  return (
    <div className="send-queue">
      <div className="send-queue-head">
        <strong>
          {fillCopy(labels.filesStaged, { count: items.length })}
        </strong>
        <span>
          {labels.totalSize}{" "}
          {formatBytes(items.reduce((sum, item) => sum + item.file.size, 0))}
        </span>
      </div>
      <ul className="send-queue-list">
        {items.map((item) => (
          <li
            className={`send-queue-item send-queue-item--${item.status}`}
            key={item.id}
          >
            <div className="send-queue-main">
              <FileUp size={15} aria-hidden="true" />
              <span className="send-queue-name">{item.file.name}</span>
              <span className="send-queue-size">
                {formatBytes(item.file.size)}
              </span>
            </div>
            <div
              aria-valuemax={100}
              aria-valuemin={0}
              aria-valuenow={sendQueuePercent(item)}
              className="send-queue-bar"
              role="progressbar"
            >
              <span style={{ width: `${sendQueuePercent(item)}%` }} />
            </div>
            <span className="send-queue-state">
              {sendQueueStateLabel(item, labels)}
            </span>
            {item.status === "failed" && item.message ? (
              <span className="send-queue-error">{item.message}</span>
            ) : null}
            {canRemove ? (
              <button
                className="send-queue-remove"
                type="button"
                onClick={() => onRemove(item.id)}
              >
                {labels.removeFile}
              </button>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  );
}

// shareFromTransfer is what a published send hands to its success panel: the
// link, the pickup code, the expiry and the record it created.
function shareFromTransfer(transfer: Transfer): TransferQueueShare {
  return {
    url: transfer.share?.url ?? "",
    expiresAt: transfer.share?.expiresAt ?? transfer.expiresAt,
    pickupCode: transfer.pickupCode,
    pasteId: transfer.pasteId,
  };
}

// guestSendOptions are the settings and draft fields one guest send carries.
// The guest token is shared through the caller's ref because one send spans
// several requests.
type GuestSendOptions = {
  tokenRef: { current: string };
  expiresInSeconds: number;
  password: string;
  title?: string;
  text?: string;
};

// createGuestSend creates one guest transfer and remembers the guest token the
// rest of the send needs. Both guest send shapes (files and text) go through it,
// so they cannot drift in which credentials they carry.
async function createGuestSend(
  options: GuestSendOptions,
  items: TransferItemInput[],
  idempotencyKey: string,
): Promise<string> {
  const created = await client.createGuestTransfer({
    guestToken: options.tokenRef.current || undefined,
    idempotencyKey,
    expiresInSeconds: options.expiresInSeconds,
    password: options.password,
    title: options.title?.trim() || undefined,
    text: options.text,
    items,
  });
  options.tokenRef.current = created.guestToken;
  return created.transfer.id;
}

// publishGuestSend publishes one guest transfer and returns what the success
// panel shows.
async function publishGuestSend(
  tokenRef: { current: string },
  transferId: string,
): Promise<TransferQueueShare> {
  const published = await client.publishGuestTransfer(
    transferId,
    tokenRef.current,
  );
  return shareFromTransfer(published.transfer);
}

// guestTransferQueueAdapter wires one guest send area to the transfer endpoints,
// so file mode and image mode upload, publish and cancel identically.
function guestTransferQueueAdapter(
  options: GuestSendOptions,
): TransferQueueAdapter {
  return {
    create: (manifest, idempotencyKey) =>
      createGuestSend(options, manifest, idempotencyKey),
    upload: async (transferId, itemId, file, onProgress, signal) => {
      await client.uploadGuestTransferItem(
        transferId,
        itemId,
        file,
        options.tokenRef.current,
        onProgress,
        { signal },
      );
    },
    publish: (transferId) => publishGuestSend(options.tokenRef, transferId),
    cancel: async (transferId) => {
      await client.cancelGuestTransfer(transferId, options.tokenRef.current);
    },
  };
}

// guestTextTransferAdapter sends text as the content of one transfer: no files
// to upload, just create and publish under the same credentials.
function guestTextTransferAdapter(options: GuestSendOptions): TextSendAdapter {
  return {
    create: (idempotencyKey) => createGuestSend(options, [], idempotencyKey),
    publish: (transferId) => publishGuestSend(options.tokenRef, transferId),
  };
}

function GuestWorkbench({
  config,
  locale,
}: {
  config: GuestUploadConfig;
  locale: Locale;
}) {
  const labels = guestWorkbenchCopy[locale] ?? guestWorkbenchCopy.en;
  const transferLabels = transferCopyFor(locale);
  const t = useMemo(() => copyFor(locale), [locale]);
  // The guest send settings live next to the files: expiry stays inside the
  // guest retention and the optional password protects whatever gets sent.
  const [sendExpirySeconds, setSendExpirySeconds] = useState(
    config.retentionSeconds,
  );
  const [sendPassword, setSendPassword] = useState("");
  useEffect(() => {
    setSendExpirySeconds((current) =>
      clampExpirySeconds(current, config.retentionSeconds),
    );
  }, [config.retentionSeconds]);
  const expiryOptions = useMemo(
    () => expiryOptionsFor(config.retentionSeconds, sendExpirySeconds, t),
    [config.retentionSeconds, sendExpirySeconds, t],
  );
  const [mode, setMode] = useState<SendMode>("file");
  const [title, setTitle] = useState("");
  const [text, setText] = useState("");
  const guestTokenRef = useRef("");
  const [status, setStatus] = useState("");
  const [limitMessage, setLimitMessage] = useState("");
  const fileInputId = useId();
  const imageInputId = useId();
  // Every guest send is the same transfer flow as the account one, so all three
  // modes share one behaviour for progress, retry and publish. The guest token
  // lives in a ref because one send spans several requests.
  const guestFileAdapter = useMemo<TransferQueueAdapter>(
    () =>
      guestTransferQueueAdapter({
        tokenRef: guestTokenRef,
        expiresInSeconds: sendExpirySeconds,
        password: sendPassword,
      }),
    [sendExpirySeconds, sendPassword],
  );
  const guestImageAdapter = useMemo<TransferQueueAdapter>(
    () =>
      guestTransferQueueAdapter({
        tokenRef: guestTokenRef,
        expiresInSeconds: sendExpirySeconds,
        password: sendPassword,
        title,
      }),
    [sendExpirySeconds, sendPassword, title],
  );
  const guestTextAdapter = useMemo<TextSendAdapter>(
    () =>
      guestTextTransferAdapter({
        tokenRef: guestTokenRef,
        expiresInSeconds: sendExpirySeconds,
        password: sendPassword,
        title,
        text,
      }),
    [sendExpirySeconds, sendPassword, title, text],
  );
  const guestFileQueue = useTransferQueue(guestFileAdapter);
  const guestImageQueue = useTransferQueue(guestImageAdapter);
  const guestText = useTextSend(guestTextAdapter);
  const guestLimits = useMemo<SendLimits>(
    () => ({
      singleFileBytes: config.singleFileBytes,
      singlePasteBytes: config.singlePasteBytes,
      attachmentsPerPasteLimit: config.attachmentsPerPasteLimit,
    }),
    [
      config.singleFileBytes,
      config.singlePasteBytes,
      config.attachmentsPerPasteLimit,
    ],
  );
  const textBytes = byteSize(text);

  // Switching modes keeps every draft: staged files, a staged image and the
  // typed text all stay where they were, so a switch cannot lose work.
  function switchSendMode(nextMode: SendMode) {
    setMode(nextMode);
    setStatus("");
  }

  function showLimit(message: string) {
    setLimitMessage(message);
  }

  // Text mode sends the body as the content of its own transfer, so it gets the
  // same link, pickup code and expiry as a file send.
  function sendGuestText() {
    if (!config.enabled) {
      setStatus(labels.disabled);
      return;
    }
    if (text.trim() === "") {
      setStatus(labels.missingText);
      return;
    }
    if (textBytes > config.singleTextBytes) {
      showLimit(labels.overText);
      return;
    }
    setStatus("");
    void guestText.send(`${title}\u0000${text}`);
  }

  // The guest file queue declares every file up front, so the share only
  // appears once the whole manifest landed.
  function stageGuestFiles(files: File[]) {
    if (files.length === 0) return;
    if (!config.enabled) {
      setStatus(labels.disabled);
      return;
    }
    const violation = checkSendLimits(guestFileQueue.items, files, guestLimits);
    if (violation) {
      const message = sendLimitCopy(violation, transferLabels);
      // Running out of room is the guest upgrade prompt; too many files is a
      // plain status, because signing in is not the fix for it.
      if (violation === "count") {
        setStatus(message);
      } else {
        showLimit(message);
      }
      return;
    }
    if (!guestFileQueue.addFiles(files)) {
      setStatus(transferLabels.manifestFrozen);
      return;
    }
    setStatus("");
  }

  // Image mode sends one image; the shared helper decides whether a pick
  // replaces a draft, starts over, or has to be refused. Running out of room is
  // the guest upgrade prompt, so that one case opens the modal.
  async function stageGuestImage(files: File[]) {
    if (!config.enabled) {
      setStatus(labels.disabled);
      return;
    }
    const result = await stageOneImage(
      guestImageQueue,
      files[0],
      guestLimits,
      transferLabels,
    );
    if (result.kind === "limit") {
      showLimit(result.message);
    } else if (result.kind === "error") {
      setStatus(result.message);
    } else if (result.kind === "staged") {
      setStatus("");
    }
  }

  // A cancel only claims success when the server confirmed it dropped the draft.
  async function cancelGuestSend(queue: TransferQueue) {
    const canceled = await queue.cancel();
    setStatus(canceled ? transferLabels.canceled : transferLabels.failed);
  }

  const activeQueue = mode === "image" ? guestImageQueue : guestFileQueue;
  const guestStatusText = sendQueueStatus(
    activeQueue.phase,
    activeQueue.counts,
    activeQueue.error,
    transferLabels,
  );
  const guestTextStatusText = textSendStatus(
    guestText.phase,
    guestText.error,
    transferLabels,
  );
  const guestTextShare = guestText.phase === "sent" ? guestText.share : null;

  async function copyGuestText(value: string, message = labels.copied) {
    if (!value) return;
    try {
      await navigator.clipboard?.writeText(value);
      setStatus(message);
    } catch {
      setStatus(value);
    }
  }

  const activePanelId = `guest-panel-${mode}`;
  const activeTabId = `guest-tab-${mode}`;
  const modalTitleId = "guest-limit-modal-title";

  return (
    <div className="guest-workbench" aria-label={labels.hint}>
      <div className="clipboard-window-bar guest-workbench-bar">
        <span />
        <span />
        <span />
        <strong>PasteBox</strong>
      </div>
      <SendModeTabs
        ariaLabel="PasteBox"
        className="guest-workbench-tabs"
        idPrefix="guest"
        mode={mode}
        onSelect={switchSendMode}
        labels={transferLabels}
      />
      <div
        aria-labelledby={activeTabId}
        className="guest-workbench-panel"
        id={activePanelId}
        onPaste={(event) => {
          const pasted = Array.from(event.clipboardData.files);
          if (pasted.length === 0) return;
          if (mode === "image") {
            void stageGuestImage(pasted);
          } else if (mode === "file") {
            stageGuestFiles(pasted);
          }
        }}
        role="tabpanel"
      >
        {mode !== "file" ? (
          <label className="guest-field">
            <span>{labels.titlePlaceholder}</span>
            <input
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              placeholder={labels.titlePlaceholder}
            />
          </label>
        ) : null}
        {mode === "text" ? (
          <label className="guest-field guest-field--text">
            <span>{transferLabels.modeText}</span>
            <textarea
              value={text}
              onChange={(event) => setText(event.target.value)}
              placeholder={labels.textPlaceholder}
            />
            <small className="guest-limit-note">
              {labels.textLimit}: {formatBytes(config.singleTextBytes)}
            </small>
          </label>
        ) : mode === "image" ? (
          <>
            <label
              aria-disabled={!guestImageQueue.canStage}
              className={`guest-upload-box ${guestImageQueue.canStage ? "" : "guest-upload-box--locked"}`}
              htmlFor={imageInputId}
              onDragOver={(event) => event.preventDefault()}
              onDrop={(event) => {
                event.preventDefault();
                void stageGuestImage(Array.from(event.dataTransfer.files));
              }}
            >
              <UploadCloud size={22} aria-hidden="true" />
              <strong>
                {guestImageQueue.items[0]?.file.name ??
                  transferLabels.chooseImage}
              </strong>
              <span>
                {guestImageQueue.canStage
                  ? transferLabels.dropImageHint
                  : sendZoneLabel(guestImageQueue.phase, transferLabels)}
              </span>
              <input
                accept="image/*"
                className="visually-hidden-file-input"
                disabled={!guestImageQueue.canStage}
                id={imageInputId}
                type="file"
                onChange={(event) => {
                  const files = Array.from(event.target.files ?? []);
                  event.target.value = "";
                  void stageGuestImage(files);
                }}
              />
              <small className="guest-limit-note">
                {labels.fileLimit}: {formatBytes(config.singleFileBytes)}
              </small>
            </label>
            <SendQueueList
              canRemove={guestImageQueue.canStage}
              items={guestImageQueue.items}
              labels={transferLabels}
              onRemove={guestImageQueue.removeFile}
            />
          </>
        ) : (
          <>
            <label
              aria-disabled={!guestFileQueue.canStage}
              className={`guest-upload-box ${guestFileQueue.canStage ? "" : "guest-upload-box--locked"}`}
              htmlFor={fileInputId}
              onDragOver={(event) => event.preventDefault()}
              onDrop={(event) => {
                event.preventDefault();
                stageGuestFiles(Array.from(event.dataTransfer.files));
              }}
            >
              <UploadCloud size={22} aria-hidden="true" />
              <strong>{transferLabels.chooseFiles}</strong>
              <span>
                {guestFileQueue.canStage
                  ? transferLabels.dropFilesHint
                  : sendZoneLabel(guestFileQueue.phase, transferLabels)}
              </span>
              <input
                className="visually-hidden-file-input"
                disabled={!guestFileQueue.canStage}
                id={fileInputId}
                multiple
                type="file"
                onChange={(event) => {
                  const files = Array.from(event.target.files ?? []);
                  event.target.value = "";
                  stageGuestFiles(files);
                }}
              />
              <small className="guest-limit-note">
                {labels.fileLimit}: {formatBytes(config.singleFileBytes)}
              </small>
            </label>
            <SendQueueList
              canRemove={guestFileQueue.canStage}
              items={guestFileQueue.items}
              labels={transferLabels}
              onRemove={guestFileQueue.removeFile}
            />
          </>
        )}
        <SendSettingsFields
          expiresInSeconds={sendExpirySeconds}
          expiryOptions={expiryOptions}
          labels={transferLabels}
          onExpiry={setSendExpirySeconds}
          onPassword={setSendPassword}
          password={sendPassword}
        />
      </div>
      <div className="guest-workbench-actions">
        <span>
          {labels.hint} ·{" "}
          {formatBytes(
            mode === "text"
              ? textBytes
              : activeQueue.items.reduce(
                  (sum, item) => sum + item.file.size,
                  0,
                ),
          )}{" "}
          / {formatBytes(config.singlePasteBytes)}
        </span>
        {mode === "text" ? (
          <button
            type="button"
            onClick={sendGuestText}
            disabled={guestText.phase === "sending"}
          >
            <Link2 size={16} aria-hidden="true" />
            {guestText.phase === "sending"
              ? transferLabels.sendingText
              : transferLabels.sendText}
          </button>
        ) : (
          <>
            {activeQueue.canSend ? (
              <button type="button" onClick={activeQueue.start}>
                <UploadCloud size={16} aria-hidden="true" />
                {fillCopy(transferLabels.sendFiles, {
                  count: activeQueue.items.length,
                })}
              </button>
            ) : null}
            {activeQueue.phase === "failed" ? (
              <button type="button" onClick={activeQueue.retry}>
                <RotateCcw size={16} aria-hidden="true" />
                {activeQueue.counts.failed > 0
                  ? transferLabels.retryFailed
                  : transferLabels.retrySend}
              </button>
            ) : null}
            {activeQueue.phase === "uploading" ||
            activeQueue.phase === "publishing" ||
            activeQueue.phase === "failed" ? (
              <button
                type="button"
                onClick={() => void cancelGuestSend(activeQueue)}
              >
                <Ban size={16} aria-hidden="true" />
                {transferLabels.cancelSend}
              </button>
            ) : null}
          </>
        )}
      </div>
      {mode === "text" ? (
        guestTextStatusText ? (
          <p className="guest-status">{guestTextStatusText}</p>
        ) : null
      ) : guestStatusText ? (
        <p className="guest-status">{guestStatusText}</p>
      ) : null}
      {mode === "text" ? (
        guestTextShare ? (
          <SendShareResult
            copyLabel={labels.copyLink}
            labels={transferLabels}
            lineClassName="guest-status"
            onCopy={(value) => void copyGuestText(value)}
            onCopyCode={(code) =>
              void copyGuestText(code, transferLabels.pickupCodeCopied)
            }
            onReset={guestText.reset}
            resetLabel={transferLabels.newSend}
            share={guestTextShare}
            showReadyLine={false}
            wrapperClassName="guest-share-block"
          />
        ) : null
      ) : activeQueue.share ? (
        <SendShareResult
          copyLabel={labels.copyLink}
          labels={transferLabels}
          lineClassName="guest-status"
          onCopy={(value) => void copyGuestText(value)}
          onCopyCode={(code) =>
            void copyGuestText(code, transferLabels.pickupCodeCopied)
          }
          onReset={activeQueue.reset}
          resetLabel={transferLabels.newSend}
          share={activeQueue.share}
          showReadyLine={false}
          wrapperClassName="guest-share-block"
        />
      ) : null}
      {status ? <p className="guest-status">{status}</p> : null}
      {limitMessage ? (
        <div className="guest-limit-backdrop" role="presentation">
          <section
            aria-modal="true"
            aria-labelledby={modalTitleId}
            className="guest-limit-modal"
            role="dialog"
          >
            <span className="eyebrow">{labels.modalEyebrow}</span>
            <h3 id={modalTitleId}>{labels.modalTitle}</h3>
            <p>{limitMessage}</p>
            <div>
              <button type="button" onClick={() => setLimitMessage("")}>
                {labels.cancel}
              </button>
              <a href="/login">{labels.login}</a>
            </div>
          </section>
        </div>
      ) : null}
    </div>
  );
}

function AuthScreen({
  mode,
  auth,
  busy,
  message,
  passwordResetLinkActive,
  onAuth,
  onLogin,
  onRegister,
  onStartRegistrationVerification,
  onGoogle,
  onGithub,
  onPasswordReset,
  onFinishPasswordReset,
  onCancelPasswordReset,
  registration,
  locale,
}: {
  mode: AuthMode;
  auth: AuthFormState;
  busy: boolean;
  message: string;
  passwordResetLinkActive: boolean;
  onAuth: (value: AuthFormState) => void;
  onLogin: () => void;
  onRegister: () => void;
  onStartRegistrationVerification: () => void;
  onGoogle: () => void;
  onGithub: () => void;
  onPasswordReset: () => void;
  onFinishPasswordReset: () => void;
  onCancelPasswordReset: () => void;
  registration?: RegistrationConfig;
  locale: Locale;
}) {
  const t = copyFor(locale);
  const content = landingContentFor(locale);
  const isRegister = mode === "register";
  const isPasswordReset = passwordResetLinkActive;
  const allowedDomains = registration?.allowedDomains ?? [];
  const registrationEmail = splitEmailForRegistration(
    auth.email,
    allowedDomains,
  );

  return (
    <main className="auth-screen product-auth-screen">
      <section className="auth-product-panel">
        <a className="brand-mark landing-brand" href="/">
          <div className="brand-icon">
            <ClipboardCopy size={22} aria-hidden="true" />
          </div>
          <div>
            <strong>PasteBox</strong>
            <span>{t("privateCloudClipboard")}</span>
          </div>
        </a>
        <div className="auth-product-copy">
          <span className="eyebrow">{content.eyebrow}</span>
          <h1>{isRegister ? content.primaryCta : content.secondaryCta}</h1>
          <p>{content.subtitle}</p>
        </div>
        <img
          alt=""
          aria-hidden="true"
          className="clay-scene auth-clay-scene"
          src={clayHeroAsset}
        />
        <div className="auth-preview-card">
          {content.features.map((feature) => (
            <article key={feature.title}>
              <span>{feature.stat}</span>
              <strong>{feature.title}</strong>
              <p>{feature.body}</p>
            </article>
          ))}
        </div>
      </section>

      <section className="auth-panel auth-form-panel">
        <div className="auth-mode-tabs" aria-label="Authentication mode">
          <a className={isRegister ? "" : "active"} href="/login">
            {t("login")}
          </a>
          <a className={isRegister ? "active" : ""} href="/register">
            {t("register")}
          </a>
        </div>

        <form
          className="auth-form"
          onSubmit={(event) => {
            event.preventDefault();
            if (isRegister) {
              onRegister();
            } else if (isPasswordReset) {
              onFinishPasswordReset();
            } else {
              onLogin();
            }
          }}
        >
          {!isPasswordReset && (!isRegister || allowedDomains.length === 0) ? (
            <label>
              {t("email")}
              <input
                autoComplete="email"
                type="email"
                value={auth.email}
                onChange={(event) =>
                  onAuth({ ...auth, email: event.target.value })
                }
              />
            </label>
          ) : null}
          {isRegister && allowedDomains.length > 0 ? (
            <div className="auth-email-grid">
              <label>
                {t("emailName")}
                <input
                  autoComplete="username"
                  value={registrationEmail.local}
                  onChange={(event) =>
                    onAuth({
                      ...auth,
                      email: buildRegistrationEmail(
                        event.target.value,
                        registrationEmail.domain,
                      ),
                    })
                  }
                />
              </label>
              <label>
                {t("emailDomain")}
                <select
                  value={registrationEmail.domain}
                  onChange={(event) =>
                    onAuth({
                      ...auth,
                      email: buildRegistrationEmail(
                        registrationEmail.local,
                        event.target.value,
                      ),
                    })
                  }
                >
                  {allowedDomains.map((domain) => (
                    <option key={domain} value={domain}>
                      @{domain}
                    </option>
                  ))}
                </select>
              </label>
            </div>
          ) : null}
          <label>
            {isPasswordReset ? t("newPassword") : t("password")}
            <input
              autoComplete={
                isRegister || isPasswordReset
                  ? "new-password"
                  : "current-password"
              }
              value={auth.password}
              type="password"
              onChange={(event) =>
                onAuth({ ...auth, password: event.target.value })
              }
            />
          </label>
          {isRegister ? (
            <label>
              {t("displayName")}
              <input
                autoComplete="name"
                value={auth.displayName}
                onChange={(event) =>
                  onAuth({ ...auth, displayName: event.target.value })
                }
              />
            </label>
          ) : null}
          {isRegister && registration?.requireEmailVerification ? (
            <div className="auth-code-row">
              <label>
                {t("registrationCode")}
                <input
                  inputMode="numeric"
                  maxLength={12}
                  value={auth.emailVerificationCode}
                  onChange={(event) =>
                    onAuth({
                      ...auth,
                      emailVerificationCode: event.target.value,
                    })
                  }
                />
              </label>
              <button
                className="auth-secondary-action"
                type="button"
                onClick={onStartRegistrationVerification}
                disabled={busy || !auth.email.trim()}
              >
                <MailCheck size={16} aria-hidden="true" />
                {t("sendRegistrationCode")}
              </button>
            </div>
          ) : null}
          {isRegister && registration?.requireTurnstile ? (
            <div className="auth-turnstile">
              <span>{t("turnstileChallenge")}</span>
              {registration.turnstileSiteKey ? (
                <TurnstileWidget
                  siteKey={registration.turnstileSiteKey}
                  locale={locale}
                  onToken={(token) =>
                    onAuth({ ...auth, turnstileToken: token })
                  }
                />
              ) : (
                <p className="status-line">{t("turnstileNotConfigured")}</p>
              )}
            </div>
          ) : null}
          <button className="auth-submit" type="submit" disabled={busy}>
            {isRegister ? (
              <Sparkles size={16} aria-hidden="true" />
            ) : (
              <KeyRound size={16} aria-hidden="true" />
            )}
            {isRegister
              ? t("register")
              : isPasswordReset
                ? t("updatePassword")
                : t("login")}
          </button>
        </form>

        {!isPasswordReset ? (
          <>
            <button
              className="auth-oauth-button"
              type="button"
              onClick={onGoogle}
              disabled={busy}
            >
              <ShieldCheck size={16} aria-hidden="true" />
              {t("google")}
            </button>
            <button
              className="auth-oauth-button"
              type="button"
              onClick={onGithub}
              disabled={busy}
            >
              <Github size={16} aria-hidden="true" />
              {t("github")}
            </button>
          </>
        ) : null}

        {isPasswordReset ? (
          <div className="auth-link-callout">
            <MailCheck size={16} aria-hidden="true" />
            <span>{t("passwordResetLinkReady")}</span>
          </div>
        ) : null}

        {!isRegister && !isPasswordReset ? (
          <button
            className="auth-forgot-button"
            type="button"
            onClick={onPasswordReset}
            disabled={busy || !auth.email.trim()}
          >
            <MailCheck size={16} aria-hidden="true" />
            <span>{t("forgotPassword")}</span>
            <strong>{t("sendResetEmail")}</strong>
          </button>
        ) : null}

        {isPasswordReset ? (
          <button
            className="auth-text-button"
            type="button"
            onClick={onCancelPasswordReset}
            disabled={busy}
          >
            {t("backToLogin")}
          </button>
        ) : null}

        {message ? <p className="status-line">{message}</p> : null}
        <PublicFooter locale={locale} />
      </section>
    </main>
  );
}

function PublicShareScreen({
  token,
  password,
  access,
  message,
  busy,
  onToken,
  onPassword,
  onOpen,
  locale,
}: {
  token: string;
  password: string;
  access: { paste: Paste; share: Share } | null;
  message: string;
  busy: boolean;
  onToken: (value: string) => void;
  onPassword: (value: string) => void;
  onOpen: () => void;
  locale: Locale;
}) {
  const t = copyFor(locale);
  // An image is fetched only when the recipient asks for it: a page that loaded
  // every image on sight would spend a download on shares that limit downloads,
  // and the reading page must not consume the sender's allowance by itself.
  const [previewID, setPreviewID] = useState("");
  const shareToken = access?.share.token ?? "";
  useEffect(() => {
    setPreviewID("");
  }, [shareToken]);
  return (
    <main className="auth-screen public-share-screen">
      <section className="auth-panel">
        <div className="brand-mark">
          <div className="brand-icon">
            <Link2 size={22} aria-hidden="true" />
          </div>
          <div>
            <strong>PasteBox</strong>
            <span>{t("sharedPaste")}</span>
          </div>
        </div>
        <img
          alt=""
          aria-hidden="true"
          className="clay-scene share-clay-scene"
          src={clayHeroAsset}
        />
        <div className="share-access-row">
          <input
            value={token}
            onChange={(event) => onToken(event.target.value)}
            placeholder={t("shareToken")}
          />
          <input
            value={password}
            onChange={(event) => onPassword(event.target.value)}
            placeholder={t("password")}
            type="password"
          />
          <button type="button" onClick={onOpen} disabled={busy || !token}>
            {t("open")}
          </button>
        </div>
        {access ? (
          <section className="shared-document">
            <div className="section-heading">
              <div>
                <h1>{access.paste.title || t("sharedPaste")}</h1>
                <span>
                  {access.share.visitCount}/{access.share.maxVisits || "∞"}{" "}
                  {t("visits")} · {formatDuration(access.paste.secondsToLive)}
                </span>
              </div>
              <button
                type="button"
                onClick={() =>
                  void navigator.clipboard?.writeText(access.paste.text)
                }
                disabled={!access.paste.text}
              >
                <ClipboardCopy size={16} aria-hidden="true" />
                {t("copy")}
              </button>
            </div>
            {access.paste.text ? <pre>{access.paste.text}</pre> : null}
            <div className="share-preview">
              {access.paste.attachments.map((attachment) => {
                const href = sharedAttachmentDownloadPath(
                  access.share.token,
                  attachment.id,
                );
                const scan = attachmentScanDetail(attachment, locale, "public");
                const isImage = attachment.contentType.startsWith("image/");
                // Anything that is not a servable image keeps the plain
                // download row, so the reading page never promises a preview it
                // cannot show.
                if (!isImage || !scan.canDownload) {
                  return (
                    <AttachmentDownloadItem
                      attachment={attachment}
                      context="public"
                      href={href}
                      icon="download"
                      key={attachment.id}
                      locale={locale}
                    />
                  );
                }
                const open = previewID === attachment.id;
                return (
                  <div className="shared-image-item" key={attachment.id}>
                    <AttachmentDownloadItem
                      attachment={attachment}
                      context="public"
                      href={href}
                      icon="download"
                      locale={locale}
                    />
                    <button
                      type="button"
                      onClick={() => setPreviewID(open ? "" : attachment.id)}
                    >
                      <ImageIcon size={16} aria-hidden="true" />
                      {open ? t("hideImage") : t("showImage")}
                    </button>
                    {open ? (
                      <img
                        alt={attachment.fileName}
                        className="shared-image"
                        src={href}
                      />
                    ) : null}
                  </div>
                );
              })}
            </div>
          </section>
        ) : null}
        {message ? <p className="status-line">{message}</p> : null}
        <PublicFooter locale={locale} />
      </section>
    </main>
  );
}

function PublicPageScreen({
  page,
  contacts,
  locale,
}: {
  page: PublicPage;
  contacts: SupportContacts | null;
  locale: Locale;
}) {
  const t = copyFor(locale);
  return (
    <main className="public-page">
      <header className="public-hero">
        <div className="brand-mark">
          <div className="brand-icon">
            <Scale size={22} aria-hidden="true" />
          </div>
          <div>
            <strong>PasteBox</strong>
            <span>{page.eyebrow}</span>
          </div>
        </div>
        <div className="public-hero-copy">
          <span className="eyebrow">{page.eyebrow}</span>
          <h1>{page.title}</h1>
          <p>{page.summary}</p>
        </div>
        <div className="public-hero-actions">
          <a className="ghost-button" href="/app">
            <ClipboardCopy size={16} aria-hidden="true" />
            {t("openApp")}
          </a>
          <a className="ghost-button" href="/support">
            <LifeBuoy size={16} aria-hidden="true" />
            {t("support")}
          </a>
        </div>
        <img
          alt=""
          aria-hidden="true"
          className="clay-scene public-clay-scene"
          src={clayFooterAsset}
        />
      </header>

      <section className="public-layout">
        <aside className="public-sidebar" aria-label={t("legalNavigation")}>
          <strong>{t("launchDocuments")}</strong>
          <nav>
            <a className={page.path === "/legal" ? "active" : ""} href="/legal">
              {t("legalHub")}
            </a>
            {publicLinks.map((link) => (
              <a
                className={page.path === link.href ? "active" : ""}
                href={link.href}
                key={link.href}
              >
                {t(link.labelKey)}
              </a>
            ))}
          </nav>
        </aside>

        <article className="public-doc">
          <div className="public-doc-meta">
            <Megaphone size={16} aria-hidden="true" />
            <span>
              {t("lastUpdated")} {page.updated}
            </span>
          </div>
          {page.path === "/support" ? (
            <section className="support-contact-card">
              <div>
                <span>{t("supportRequests")}</span>
                {contacts?.supportEmail ? (
                  <a href={`mailto:${contacts.supportEmail}`}>
                    {contacts.supportEmail}
                  </a>
                ) : (
                  <strong>{t("supportContactLoading")}</strong>
                )}
              </div>
              <div>
                <span>{t("abuseRequests")}</span>
                {contacts?.abuseEmail ? (
                  <a href={`mailto:${contacts.abuseEmail}`}>
                    {contacts.abuseEmail}
                  </a>
                ) : (
                  <strong>{t("abuseContactLoading")}</strong>
                )}
              </div>
            </section>
          ) : null}
          {page.sections.map((section) => (
            <section className="public-section" key={section.heading}>
              <h2>{section.heading}</h2>
              {section.body.map((paragraph) => (
                <p key={paragraph}>{paragraph}</p>
              ))}
              {section.items ? (
                <ul>
                  {section.items.map((item) => (
                    <li key={item}>{item}</li>
                  ))}
                </ul>
              ) : null}
            </section>
          ))}
          <footer className="public-doc-footer">
            <FileText size={16} aria-hidden="true" />
            <span>{t("publicDocFooter")}</span>
          </footer>
        </article>
      </section>
    </main>
  );
}

function footerGroupsFor(locale: Locale) {
  const t = copyFor(locale);
  return [
    {
      title: t("footerLegal"),
      links: [
        { href: "/legal", label: t("legalHub") },
        { href: "/legal/terms", label: t("terms") },
        { href: "/legal/privacy", label: t("privacy") },
        { href: "/legal/cookies", label: t("cookies") },
      ],
    },
    {
      title: t("footerTrust"),
      links: [
        { href: "/legal/refund", label: t("refund") },
        { href: "/legal/abuse", label: t("abuseDmca") },
        { href: "/status", label: t("status") },
      ],
    },
    {
      title: t("footerSupport"),
      links: [{ href: "/support", label: t("support") }],
    },
  ];
}

function WorkspaceFooter({ locale }: { locale: Locale }) {
  const t = copyFor(locale);
  const groups = footerGroupsFor(locale);
  return (
    <footer className="workspace-footer">
      <UsageBoundaryNotice locale={locale} />
      <nav aria-label={t("legalNavigation")}>
        {groups.map((group) => (
          <section className="workspace-footer-group" key={group.title}>
            <strong>{group.title}</strong>
            <div>
              {group.links.map((link) => (
                <a href={link.href} key={link.href}>
                  {link.label}
                </a>
              ))}
            </div>
          </section>
        ))}
      </nav>
    </footer>
  );
}

function PublicFooter({
  compact = false,
  locale,
}: {
  compact?: boolean;
  locale: Locale;
}) {
  const t = copyFor(locale);
  const groups = footerGroupsFor(locale);
  return (
    <footer className={compact ? "public-footer compact" : "public-footer"}>
      <img
        alt=""
        aria-hidden="true"
        className="clay-footer-art"
        src={clayFooterAsset}
      />
      <UsageBoundaryNotice locale={locale} />
      <nav aria-label={t("legalNavigation")}>
        {groups.map((group) => (
          <section className="public-footer-group" key={group.title}>
            <strong>{group.title}</strong>
            <div>
              {group.links.map((link) => (
                <a href={link.href} key={link.href}>
                  {link.label}
                </a>
              ))}
            </div>
          </section>
        ))}
      </nav>
    </footer>
  );
}

function PasteEditor({
  paste,
  draft,
  onDraft,
  onSave,
  tagLimit,
  canEditTags,
  tagsReadOnly,
  locale,
}: {
  paste?: Paste;
  draft: { id: string; title: string; text: string; tags: string };
  onDraft: (value: {
    id: string;
    title: string;
    text: string;
    tags: string;
  }) => void;
  onSave: () => void;
  tagLimit: number;
  canEditTags: boolean;
  tagsReadOnly: boolean;
  locale: Locale;
}) {
  const t = copyFor(locale);
  const tagCount = parseTagInput(draft.tags).length;
  const tagNote = canEditTags
    ? `${tagCount}/${tagLimit}`
    : tagsReadOnly
      ? t("tagReadOnly")
      : t("upgradeForTags");
  return (
    <Panel
      title={t("edit")}
      meta={paste ? paste.title || paste.id : t("noPasteSelected")}
    >
      <div className="form-grid single">
        <input
          value={draft.title}
          onChange={(event) => onDraft({ ...draft, title: event.target.value })}
          placeholder={t("title")}
          disabled={!paste}
        />
        <textarea
          value={draft.text}
          onChange={(event) => onDraft({ ...draft, text: event.target.value })}
          placeholder={t("text")}
          disabled={!paste}
        />
        <input
          value={draft.tags}
          onChange={(event) => onDraft({ ...draft, tags: event.target.value })}
          placeholder={t("tags")}
          disabled={!paste || !canEditTags}
        />
        <small className="tag-field-note">{tagNote}</small>
      </div>
      <button type="button" onClick={onSave} disabled={!paste}>
        {t("save")}
      </button>
    </Panel>
  );
}

function ShareBox({
  paste,
  draft,
  token,
  access,
  onDraft,
  onCreate,
  onOpen,
  locale,
}: {
  paste?: Paste;
  draft: ShareDraft;
  token: string;
  access: { paste: Paste; share: Share } | null;
  onDraft: (value: ShareDraft) => void;
  onCreate: () => void;
  onOpen: () => void;
  locale: Locale;
}) {
  const t = copyFor(locale);
  return (
    <Panel
      title={t("share")}
      meta={paste ? paste.title || paste.id : t("noPasteSelected")}
    >
      <div className="form-grid single">
        <input
          value={draft.password}
          onChange={(event) =>
            onDraft({ ...draft, password: event.target.value })
          }
          placeholder={t("password")}
        />
        <label className="check-row">
          <input
            type="checkbox"
            checked={draft.loginRequired}
            onChange={(event) =>
              onDraft({ ...draft, loginRequired: event.target.checked })
            }
          />
          {t("loginRequired")}
        </label>
        <input
          value={draft.maxVisits}
          min={0}
          type="number"
          onChange={(event) =>
            onDraft({ ...draft, maxVisits: Number(event.target.value) })
          }
          placeholder={t("maxVisits")}
        />
        <input
          value={draft.maxDownloads}
          min={0}
          type="number"
          onChange={(event) =>
            onDraft({ ...draft, maxDownloads: Number(event.target.value) })
          }
          placeholder={t("maxDownloads")}
        />
        <select
          value={draft.expiresInSeconds}
          onChange={(event) =>
            onDraft({ ...draft, expiresInSeconds: Number(event.target.value) })
          }
        >
          <option value={24 * 60 * 60}>{t("duration24Hours")}</option>
          <option value={7 * 24 * 60 * 60}>{t("duration7Days")}</option>
          <option value={30 * 24 * 60 * 60}>{t("duration30Days")}</option>
        </select>
      </div>
      <div className="button-row">
        <button type="button" onClick={onCreate} disabled={!paste}>
          <Link2 size={16} aria-hidden="true" />
          {t("create")}
        </button>
        <button type="button" onClick={onOpen} disabled={!token}>
          {t("open")}
        </button>
      </div>
      {token ? <code>{token}</code> : null}
      {access ? (
        <div className="share-preview">
          <article className="list-card">
            <div>
              <strong>{access.paste.title || t("sharedPaste")}</strong>
              <span>
                {access.share.visitCount} {t("visits")} ·{" "}
                {access.share.downloadCount} {t("downloads")}
              </span>
              <span>
                {access.paste.textPreview ||
                  `${access.paste.attachments.length} ${t("attachmentsCount")}`}
              </span>
            </div>
          </article>
          {access.paste.attachments.map((attachment) => (
            <AttachmentDownloadItem
              attachment={attachment}
              context="public"
              href={sharedAttachmentDownloadPath(
                access.share.token,
                attachment.id,
              )}
              icon="download"
              key={attachment.id}
              locale={locale}
            />
          ))}
        </div>
      ) : null}
    </Panel>
  );
}

function AttachmentDownloadItem({
  attachment,
  context,
  href,
  icon,
  locale,
}: {
  attachment: Attachment;
  context: "owner" | "public";
  href: string;
  icon: "file" | "download";
  locale: Locale;
}) {
  const scan = attachmentScanDetail(attachment, locale, context);
  const iconNode =
    icon === "download" ? (
      <Download size={14} aria-hidden="true" />
    ) : (
      <FileUp size={14} aria-hidden="true" />
    );
  const content = (
    <>
      <span className="attachment-link-main">
        {iconNode}
        <span>{attachment.fileName}</span>
      </span>
      <span className={`scan-badge scan-badge--${scan.tone}`}>
        {scan.label}
      </span>
      <span className="attachment-scan-note">{scan.description}</span>
    </>
  );

  if (!scan.canDownload) {
    return (
      <span
        className="attachment-link attachment-link--blocked"
        aria-disabled="true"
      >
        {content}
      </span>
    );
  }

  return (
    <a className="attachment-link" href={href}>
      {content}
    </a>
  );
}

function Panel({
  className,
  title,
  meta,
  children,
}: {
  className?: string;
  title: string;
  meta: string;
  children: ReactNode;
}) {
  return (
    <section className={className ? `panel ${className}` : "panel"}>
      <div className="section-heading">
        <h2>{title}</h2>
        <span>{meta}</span>
      </div>
      {children}
    </section>
  );
}

function navClass(current: View, view: View): string {
  return current === view ? "nav-item active" : "nav-item";
}

function parseTagInput(value: string): string[] {
  const seen = new Set<string>();
  const tags: string[] = [];
  for (const raw of value.split(",")) {
    const tag = raw.trim().toLowerCase();
    if (tag && !seen.has(tag)) {
      seen.add(tag);
      tags.push(tag);
    }
  }
  return tags.sort();
}

function searchParams(
  query: string,
  filter: string,
  tag: string,
): URLSearchParams {
  const params = new URLSearchParams();
  if (query) params.set("query", query);
  if (filter) params.set("filter", filter);
  if (tag) params.set("tag", tag);
  return params;
}

export default App;
