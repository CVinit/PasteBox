import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  Archive,
  ClipboardCopy,
  Clock3,
  CreditCard,
  Download,
  FileText,
  FileUp,
  Filter,
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
  Sparkles,
  Star,
  TimerReset,
  Trash2,
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
  type ApiError,
  type AuditLog,
  type Order,
  type Paste,
  type PlanCatalog,
  type Price,
  type Quota,
  type Report,
  type Share,
  type SupportContacts,
  type User,
  type WebhookEvent,
} from "./api";
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

type AdminData = {
  users: User[];
  pastes: Paste[];
  attachments: AdminAttachment[];
  shares: AdminShare[];
  orders: Order[];
  queues: AdminQueues | null;
  webhookEvents: WebhookEvent[];
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

type AuthLinkKind = "email-verification" | "magic" | "password-reset";

type AuthLink = {
  kind: AuthLinkKind;
  token: string;
};

type AuthMode = "login" | "register";

type AuthFormState = {
  email: string;
  password: string;
  displayName: string;
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

const emptyAdminData: AdminData = {
  users: [],
  pastes: [],
  attachments: [],
  shares: [],
  orders: [],
  queues: null,
  webhookEvents: [],
};

const supportedLocales: Array<{ value: Locale; label: string }> = [
  { value: "en", label: "English" },
  { value: "zh-CN", label: "简体中文" },
  { value: "zh-TW", label: "繁體中文" },
  { value: "es", label: "Español" },
];

const viewSummaries: Record<Locale, Record<View, ViewSummary>> = {
  en: {
    inbox: {
      eyebrow: "Private transfer desk",
      title: "Capture. Scan. Share.",
      description:
        "PasteBox keeps active clips, expiring files, and share controls visible in one operational workspace.",
    },
    shared: {
      eyebrow: "Link control",
      title: "Shared links, under control.",
      description:
        "Review visits, download counts, expiry windows, and revoke risky links before they drift.",
    },
    billing: {
      eyebrow: "Plan and payments",
      title: "Payments with lifecycle detail.",
      description:
        "Stripe and Epusdt orders show lifecycle detail instead of raw status strings.",
    },
    settings: {
      eyebrow: "Account operations",
      title: "Account operations in one place.",
      description:
        "Manage identity, export data, report abuse, and handle deletion requests from one place.",
    },
    admin: {
      eyebrow: "Launch control room",
      title: "Launch signals at a glance.",
      description:
        "Monitor production surfaces that gate public beta readiness and abuse response.",
    },
  },
  "zh-CN": {
    inbox: {
      eyebrow: "私有传输台",
      title: "捕获、扫描、分享。",
      description:
        "PasteBox 在同一个操作工作区里展示活动内容、即将过期的文件和分享控制。",
    },
    shared: {
      eyebrow: "链接控制",
      title: "分享链接，始终可控。",
      description: "查看访问、下载、过期窗口，并在风险扩散前撤销链接。",
    },
    billing: {
      eyebrow: "套餐与支付",
      title: "带生命周期细节的支付。",
      description: "Stripe 和 Epusdt 订单显示完整生命周期，而不是原始状态字符串。",
    },
    settings: {
      eyebrow: "账号操作",
      title: "账号操作集中处理。",
      description: "在一个位置管理身份、导出数据、举报滥用和处理删除请求。",
    },
    admin: {
      eyebrow: "上线控制室",
      title: "上线信号一目了然。",
      description: "监控决定公开 beta 就绪度和滥用响应能力的生产表面。",
    },
  },
  "zh-TW": {
    inbox: {
      eyebrow: "私有傳輸台",
      title: "擷取、掃描、分享。",
      description:
        "PasteBox 在同一個操作工作區裡展示作用中內容、即將過期的檔案和分享控制。",
    },
    shared: {
      eyebrow: "連結控制",
      title: "分享連結，始終可控。",
      description: "檢視訪問、下載、過期視窗，並在風險擴散前撤銷連結。",
    },
    billing: {
      eyebrow: "方案與付款",
      title: "帶生命週期細節的付款。",
      description: "Stripe 和 Epusdt 訂單顯示完整生命週期，而不是原始狀態字串。",
    },
    settings: {
      eyebrow: "帳號操作",
      title: "帳號操作集中處理。",
      description: "在一個位置管理身分、匯出資料、檢舉濫用和處理刪除請求。",
    },
    admin: {
      eyebrow: "上線控制室",
      title: "上線訊號一目了然。",
      description: "監控決定公開 beta 就緒度和濫用回應能力的生產表面。",
    },
  },
  es: {
    inbox: {
      eyebrow: "Mesa privada de transferencia",
      title: "Captura. Escanea. Comparte.",
      description:
        "PasteBox mantiene recortes activos, archivos por vencer y controles de enlace en un solo espacio operativo.",
    },
    shared: {
      eyebrow: "Control de enlaces",
      title: "Enlaces compartidos bajo control.",
      description:
        "Revisa visitas, descargas, vencimientos y revoca enlaces riesgosos antes de que se propaguen.",
    },
    billing: {
      eyebrow: "Planes y pagos",
      title: "Pagos con detalle de ciclo de vida.",
      description:
        "Los pedidos de Stripe y Epusdt muestran estado operativo, no solo cadenas sin contexto.",
    },
    settings: {
      eyebrow: "Operaciones de cuenta",
      title: "Cuenta, datos y soporte en un lugar.",
      description:
        "Gestiona identidad, exportaciones, reportes de abuso y solicitudes de eliminación.",
    },
    admin: {
      eyebrow: "Sala de lanzamiento",
      title: "Señales de lanzamiento al instante.",
      description:
        "Monitorea las superficies que bloquean beta pública y respuesta ante abuso.",
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
    case "/magic":
      return { kind: "magic", token };
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
  if (normalized === "zh-cn" || normalized === "zh-sg" || normalized.startsWith("zh")) {
    return "zh-CN";
  }
  if (normalized.startsWith("es")) return "es";
  return "en";
}

function isChineseLocale(locale: Locale): boolean {
  return locale.startsWith("zh");
}

function browserPreferredLocale(): Locale {
  if (typeof navigator === "undefined") return "en";
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
    normalized === "/magic" ||
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
    privateCloudClipboard: "Private cloud clipboard",
    sharedPaste: "Shared paste",
    email: "Email",
    password: "Password",
    displayName: "Display name",
    login: "Login",
    register: "Register",
    google: "Google",
    verificationToken: "verification token",
    verify: "Verify",
    magicLink: "Magic link",
    magicToken: "magic link token",
    useToken: "Use token",
    manualTokenFallback: "Manual token fallback",
    reset: "Reset",
    resetToken: "reset token",
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
    stripeUsdtPayments: "Stripe and USDT payment lifecycle",
    billingSupport: "Billing support",
    billingSupportBody:
      "Refunds, duplicate charges, stuck Epusdt payments, and manual review requests are handled through the refund policy and support intake.",
    refundPolicy: "Refund Policy",
    support: "Support",
    legalHub: "Legal hub",
    terms: "Terms",
    refund: "Refund",
    abuseDmca: "Abuse/DMCA",
    cookies: "Cookies",
    status: "Status",
    deletion: "Deletion",
    openApp: "Open app",
    launchDocuments: "Launch documents",
    legalNavigation: "Legal navigation",
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
    magicLinkIssued: "Magic link issued",
    signedInMagic: "Signed in with magic link",
    passwordResetLinkReady:
      "Enter a new password to finish resetting your account.",
    signedOut: "Signed out",
    allSessionsSignedOut: "All sessions signed out",
    passwordResetIssued: "Password reset issued",
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
  },
  "zh-CN": {
    privateCloudClipboard: "私有云剪切板",
    sharedPaste: "分享内容",
    email: "邮箱",
    password: "密码",
    displayName: "显示名",
    login: "登录",
    register: "注册",
    google: "Google",
    verificationToken: "邮箱验证码",
    verify: "验证",
    magicLink: "魔法链接",
    magicToken: "魔法链接令牌",
    useToken: "使用令牌",
    manualTokenFallback: "手动令牌备用入口",
    reset: "重置",
    resetToken: "重置令牌",
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
    create: "创建",
    upload: "上传",
    recentPastes: "最近内容",
    untitledPaste: "未命名内容",
    createSecureDrop: "创建安全投放。",
    titleThisPaste: "为这个内容命名",
    pasteTextPlaceholder: "在此粘贴文本、备注、凭据或传输上下文。",
    tagsSeparatedByComma: "用英文逗号分隔标签",
    dropOrChooseFile: "拖放或选择文件",
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
    stripeUsdtPayments: "Stripe 和 USDT 支付状态",
    billingSupport: "支付支持",
    billingSupportBody:
      "退款、重复扣款、卡住的 Epusdt 支付和人工审核请求会通过退款政策和支持入口处理。",
    refundPolicy: "退款政策",
    support: "支持",
    legalHub: "法律中心",
    terms: "条款",
    refund: "退款",
    abuseDmca: "滥用/DMCA",
    cookies: "Cookie",
    status: "状态",
    deletion: "删除",
    openApp: "打开应用",
    launchDocuments: "上线文档",
    legalNavigation: "法律导航",
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
    magicLinkIssued: "魔法链接已签发",
    signedInMagic: "已通过魔法链接登录",
    passwordResetLinkReady: "请输入新密码以完成账号密码重置。",
    signedOut: "已退出",
    allSessionsSignedOut: "所有会话已退出",
    passwordResetIssued: "密码重置已签发",
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
  },
};

const copy: Record<Locale, Record<string, string>> = {
  en: baseCopy.en,
  "zh-CN": baseCopy["zh-CN"],
  "zh-TW": {
    ...baseCopy["zh-CN"],
    privateCloudClipboard: "私有雲剪貼簿",
    sharedPaste: "分享內容",
    email: "電子郵件",
    password: "密碼",
    displayName: "顯示名稱",
    login: "登入",
    register: "註冊",
    verificationToken: "電子郵件驗證碼",
    verify: "驗證",
    magicLink: "魔法連結",
    magicToken: "魔法連結權杖",
    useToken: "使用權杖",
    manualTokenFallback: "手動權杖備用入口",
    reset: "重設",
    resetToken: "重設權杖",
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
    create: "建立",
    upload: "上傳",
    recentPastes: "最近內容",
    untitledPaste: "未命名內容",
    createSecureDrop: "建立安全投放。",
    titleThisPaste: "為這個內容命名",
    pasteTextPlaceholder: "在此貼上文字、備註、憑證或傳輸上下文。",
    tagsSeparatedByComma: "用英文逗號分隔標籤",
    dropOrChooseFile: "拖放或選擇檔案",
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
    abuseDmca: "濫用/DMCA",
    cookies: "Cookie",
    status: "狀態",
    deletion: "刪除",
    openApp: "開啟應用",
    launchDocuments: "上線文件",
    legalNavigation: "法律導覽",
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
    magicLinkIssued: "魔法連結已簽發",
    signedInMagic: "已透過魔法連結登入",
    passwordResetLinkReady: "請輸入新密碼以完成帳號密碼重設。",
    signedOut: "已登出",
    allSessionsSignedOut: "所有工作階段已登出",
    passwordResetIssued: "密碼重設已簽發",
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
    privateCloudClipboard: "Portapapeles privado en la nube",
    sharedPaste: "Paste compartido",
    email: "Correo",
    password: "Contraseña",
    displayName: "Nombre visible",
    login: "Iniciar sesión",
    register: "Registrarse",
    google: "Google",
    verificationToken: "token de verificación",
    verify: "Verificar",
    magicLink: "Enlace mágico",
    magicToken: "token de enlace mágico",
    useToken: "Usar token",
    manualTokenFallback: "Entrada manual de token",
    reset: "Restablecer",
    resetToken: "token de restablecimiento",
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
    abuseDmca: "Abuso/DMCA",
    cookies: "Cookies",
    status: "Estado",
    deletion: "Eliminación",
    openApp: "Abrir app",
    launchDocuments: "Documentos de lanzamiento",
    legalNavigation: "Navegación legal",
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
    magicLinkIssued: "Enlace mágico emitido",
    signedInMagic: "Sesión iniciada con enlace mágico",
    passwordResetLinkReady:
      "Ingresa una contraseña nueva para terminar el restablecimiento.",
    signedOut: "Sesión cerrada",
    allSessionsSignedOut: "Todas las sesiones cerradas",
    passwordResetIssued: "Restablecimiento emitido",
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

function landingContentFor(locale: Locale): LandingContent {
  if (locale === "zh-TW") {
    return {
      navProduct: "產品",
      navSecurity: "安全",
      navPricing: "方案",
      eyebrow: "跨裝置線上剪貼簿",
      title: "把文字、檔案和分享控制放進同一個乾淨工作台。",
      subtitle:
        "PasteBox 結合線上剪貼簿的快速輸入體驗與產品級分享、掃描、到期和帳號控制。",
      primaryCta: "免費註冊",
      secondaryCta: "登入",
      workspaceLabel: "PasteBox 工作台",
      workspaceTitle: "貼上內容，設定期限，生成可控分享。",
      workspaceBody:
        "緊湊卡片、明確輸入框、掃描狀態和分享限制都在同一屏，適合快速跨設備傳輸。",
      features: [
        {
          title: "快速貼上",
          body: "像 online clipboard 一樣直接輸入，但保留私有帳號空間。",
          stat: "6s",
        },
        {
          title: "可控分享",
          body: "密碼、訪問次數、下載上限和過期時間都可在建立時設定。",
          stat: "4x",
        },
        {
          title: "檔案掃描",
          body: "附件在公開下載前顯示掃描狀態，降低誤分享風險。",
          stat: "safe",
        },
      ],
      steps: [
        { title: "貼上", body: "保存文字、連結、憑證片段或交付說明。" },
        { title: "附檔", body: "拖放檔案到同一條內容，保留上下文。" },
        { title: "分享", body: "生成限時連結，之後仍可撤銷。" },
      ],
    };
  }

  if (locale === "zh-CN") {
    return {
      navProduct: "产品",
      navSecurity: "安全",
      navPricing: "套餐",
      eyebrow: "跨设备在线剪切板",
      title: "把文字、文件和分享控制放进同一个清爽工作台。",
      subtitle:
        "PasteBox 结合在线剪切板的快速输入体验与产品级分享、扫描、到期和账号控制。",
      primaryCta: "免费注册",
      secondaryCta: "登录",
      workspaceLabel: "PasteBox 工作台",
      workspaceTitle: "粘贴内容，设置期限，生成可控分享。",
      workspaceBody:
        "紧凑卡片、清晰输入框、扫描状态和分享限制都在同一屏，适合快速跨设备传输。",
      features: [
        {
          title: "快速粘贴",
          body: "像 online clipboard 一样直接输入，但保留私有账号空间。",
          stat: "6s",
        },
        {
          title: "可控分享",
          body: "密码、访问次数、下载上限和过期时间都可在创建时设置。",
          stat: "4x",
        },
        {
          title: "文件扫描",
          body: "附件在公开下载前展示扫描状态，降低误分享风险。",
          stat: "safe",
        },
      ],
      steps: [
        { title: "粘贴", body: "保存文本、链接、凭据片段或交付说明。" },
        { title: "附加文件", body: "拖放文件到同一条内容，保留上下文。" },
        { title: "分享", body: "生成限时链接，之后仍可撤销。" },
      ],
    };
  }

  if (locale === "es") {
    return {
      navProduct: "Producto",
      navSecurity: "Seguridad",
      navPricing: "Planes",
      eyebrow: "Portapapeles online entre dispositivos",
      title: "Texto, archivos y control de enlaces en un solo escritorio limpio.",
      subtitle:
        "PasteBox combina una captura rápida tipo online clipboard con enlaces privados, escaneo, vencimientos y cuenta.",
      primaryCta: "Registrarse gratis",
      secondaryCta: "Iniciar sesión",
      workspaceLabel: "Escritorio PasteBox",
      workspaceTitle: "Pega contenido, define vencimiento y comparte con control.",
      workspaceBody:
        "Tarjetas compactas, campos visibles, estado de escaneo y límites de enlace en una sola pantalla.",
      features: [
        {
          title: "Pega rápido",
          body: "Captura directa con espacio privado de cuenta.",
          stat: "6s",
        },
        {
          title: "Enlaces controlados",
          body: "Contraseña, visitas, descargas y vencimiento al crear.",
          stat: "4x",
        },
        {
          title: "Escaneo de archivos",
          body: "Estado claro antes de permitir descargas públicas.",
          stat: "safe",
        },
      ],
      steps: [
        { title: "Pega", body: "Guarda texto, enlaces, credenciales o contexto." },
        { title: "Adjunta", body: "Suelta archivos junto al mismo contenido." },
        { title: "Comparte", body: "Crea enlaces temporales y revócalos luego." },
      ],
    };
  }

  return {
    navProduct: "Product",
    navSecurity: "Security",
    navPricing: "Pricing",
    eyebrow: "Cross-device online clipboard",
    title: "Put text, files, and share controls in one clean workspace.",
    subtitle:
      "PasteBox blends the speed of an online clipboard with product-grade sharing, scanning, expiry, and account controls.",
    primaryCta: "Register free",
    secondaryCta: "Login",
    workspaceLabel: "PasteBox workspace",
    workspaceTitle: "Paste content, set expiry, and share with control.",
    workspaceBody:
      "Compact cards, clear inputs, scan state, and link limits stay visible on one screen for quick cross-device transfer.",
    features: [
      {
        title: "Fast paste",
        body: "Direct entry like an online clipboard, backed by private account space.",
        stat: "6s",
      },
      {
        title: "Controlled links",
        body: "Set password, visit caps, download caps, and expiry before sharing.",
        stat: "4x",
      },
      {
        title: "File scanning",
        body: "Show attachment scan state before public downloads are available.",
        stat: "safe",
      },
    ],
    steps: [
      { title: "Paste", body: "Save text, links, credential snippets, or handoff notes." },
      { title: "Attach", body: "Drop files into the same paste and keep context together." },
      { title: "Share", body: "Create expiring links and revoke them later." },
    ],
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
    locale === "es" ? "Riesgo" : locale === "zh-TW" ? "風險" : isChineseLocale(locale) ? "风险" : "Risk";
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
  const [user, setUser] = useState<User | null>(null);
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
  const [view, setView] = useState<View>("inbox");
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("all");
  const [draft, setDraft] = useState<Draft>(defaultDraft);
  const [selectedPasteId, setSelectedPasteId] = useState<string>("");
  const [editDraft, setEditDraft] = useState({
    id: "",
    title: "",
    text: "",
    tags: "",
  });
  const [shareDraft, setShareDraft] = useState<ShareDraft>(defaultShareDraft);
  const [shareToken, setShareToken] = useState("");
  const [publicShareToken, setPublicShareToken] = useState(shareTokenFromPath);
  const [publicSharePassword, setPublicSharePassword] = useState("");
  const [shareAccess, setShareAccess] = useState<{
    paste: Paste;
    share: Share;
  } | null>(null);
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
  });
  const [magicToken, setMagicToken] = useState("");
  const [resetToken, setResetToken] = useState("");
  const [profileDraft, setProfileDraft] = useState({
    displayName: "",
    language: browserLocale,
  });
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [supportContacts, setSupportContacts] =
    useState<SupportContacts | null>(null);
  const locale = useMemo(
    () => localeFor(user?.language ?? browserLocale),
    [user?.language],
  );
  const t = useMemo(() => copyFor(locale), [locale]);
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
  const shouldProbeSession = Boolean(authLink || publicShareRoute || workspaceRoute);

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
      await refreshAuthed();
    }
  }, []);

  const refreshAuthed = useCallback(async () => {
    const [quotaResult, pasteResult, shareResult, orderResult] =
      await Promise.allSettled([
        client.quota(),
        client.pastes(searchParams(query, filter)),
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
  }, [filter, query, selectedPasteId]);

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
      webhookEvents:
        webhooks.status === "fulfilled" ? webhooks.value.webhookEvents : [],
    });
    if (logs.status === "fulfilled") setAuditLogs(logs.value.auditLogs);
  }, [user?.role]);

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
    if (user) void refreshAuthed();
  }, [filter, publicPage, query, user]);

  useEffect(() => {
    if (!authLink || publicPage) return;

    const link = authLink;
    setAuthLink(null);
    clearAuthLinkTokenFromLocation();

    if (link.kind === "password-reset") {
      setResetToken(link.token);
      setPasswordResetLinkActive(true);
      setMessage(t("passwordResetLinkReady"));
      return;
    }

    async function completeAuthLink() {
      if (link.kind === "email-verification") {
        const updated = await run(
          () => client.finishEmailVerification(link.token),
          t("emailVerified"),
        );
        if (updated) {
          await applyVerifiedEmail(updated);
        }
        return;
      }

      const result = await run(
        () => client.finishMagic(link.token),
        t("signedInMagic"),
      );
      if (result) {
        setUser(result.user);
        setMagicToken("");
        setVerificationToken("");
        setProfileDraft({
          displayName: result.user.displayName,
          language: localeFor(result.user.language),
        });
        moveToWorkspacePath();
        await refreshAuthed();
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
  ): Promise<T | null> {
    setBusy(true);
    setMessage("");
    try {
      const result = await action();
      setMessage(success);
      return result;
    } catch (error) {
      const apiError = error as ApiError;
      setMessage(apiError.message || t("requestFailed"));
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
    const result = await run(
      () => client.register({ ...auth, language: locale }),
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

  function googleOAuth() {
    window.location.assign(client.googleOAuthStartPath("/app"));
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

  async function startMagic() {
    const result = await run(
      () => client.startMagic(auth.email),
      t("magicLinkIssued"),
    );
    if (result) setMagicToken(result.devToken ?? "");
  }

  async function finishMagic() {
    const result = await run(
      () => client.finishMagic(magicToken),
      t("signedInMagic"),
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
    if (result) setResetToken(result.devToken ?? "");
  }

  async function finishPasswordReset() {
    const result = await run(
      () => client.finishPasswordReset(resetToken, auth.password),
      t("passwordUpdated"),
    );
    if (result) {
      setResetToken("");
      setPasswordResetLinkActive(false);
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
            .split(",")
            .map((tag) => tag.trim())
            .filter(Boolean),
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

  async function uploadFile(file: File) {
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
          tags: editDraft.tags
            .split(",")
            .map((tag) => tag.trim())
            .filter(Boolean),
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
    const updated = await run(() => client.cancelDelete(), t("deletionCanceled"));
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
    const updated = await run(
      () => client.updateMe(profileDraft),
      t("profileUpdated"),
    );
    if (updated) setUser(updated);
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

  if (publicPage) {
    return (
      <PublicPageScreen
        page={publicPage}
        contacts={supportContacts}
        locale={browserLocale}
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
        locale={browserLocale}
      />
    );
  }

  if (!user) {
    if (authRoute) {
      return (
        <AuthScreen
          mode={authRoute}
          auth={auth}
          busy={busy}
          message={message}
          magicToken={magicToken}
          resetToken={resetToken}
          verificationToken={verificationToken}
          passwordResetLinkActive={passwordResetLinkActive}
          onAuth={setAuth}
          onLogin={() => void login()}
          onRegister={() => void register()}
          onGoogle={googleOAuth}
          onStartMagic={() => void startMagic()}
          onFinishMagic={() => void finishMagic()}
          onMagicToken={setMagicToken}
          onPasswordReset={() => void passwordReset()}
          onFinishPasswordReset={() => void finishPasswordReset()}
          onResetToken={setResetToken}
          onVerificationToken={setVerificationToken}
          onFinishVerification={() => void finishVerification()}
          locale={browserLocale}
        />
      );
    }

    return (
      <LandingPage
        locale={browserLocale}
        plans={catalog?.plans ?? []}
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
        <button className="ghost-button" type="button" onClick={logoutAll}>
          <LogOut size={16} aria-hidden="true" />
          {t("logoutAll")}
        </button>
        <PublicFooter compact locale={locale} />
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
              <input
                className="title-input"
                value={draft.title}
                onChange={(event) =>
                  setDraft({ ...draft, title: event.target.value })
                }
                placeholder={t("titleThisPaste")}
              />
              <textarea
                value={draft.text}
                onChange={(event) =>
                  setDraft({ ...draft, text: event.target.value })
                }
                onPaste={(event) => {
                  const file = event.clipboardData.files.item(0);
                  if (file) void uploadFile(file);
                }}
                placeholder={t("pasteTextPlaceholder")}
              />
              <div className="composer-controls">
                <label>
                  <Clock3 size={16} aria-hidden="true" />
                  <select
                    value={draft.expiresInSeconds}
                    onChange={(event) =>
                      setDraft({
                        ...draft,
                        expiresInSeconds: Number(event.target.value),
                      })
                    }
                  >
                    <option value={24 * 60 * 60}>{t("duration24Hours")}</option>
                    <option value={7 * 24 * 60 * 60}>{t("duration7Days")}</option>
                    <option value={30 * 24 * 60 * 60}>
                      {t("duration30Days")}
                    </option>
                    <option value={180 * 24 * 60 * 60}>
                      {t("duration180Days")}
                    </option>
                  </select>
                </label>
                <input
                  value={draft.tags}
                  onChange={(event) =>
                    setDraft({ ...draft, tags: event.target.value })
                  }
                  placeholder={t("tagsSeparatedByComma")}
                />
                <button type="button" onClick={createPaste} disabled={busy}>
                  <Sparkles size={16} aria-hidden="true" />
                  {t("create")}
                </button>
              </div>
              <label
                className="drop-zone"
                onDragOver={(event) => event.preventDefault()}
                onDrop={(event) => {
                  event.preventDefault();
                  const file = event.dataTransfer.files.item(0);
                  if (file) void uploadFile(file);
                }}
              >
                <UploadCloud size={20} aria-hidden="true" />
                <input
                  type="file"
                  onChange={(event) =>
                    event.target.files?.[0] &&
                    void uploadFile(event.target.files[0])
                  }
                />
                {t("dropOrChooseFile")}
              </label>
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
                locale={locale}
              />
              <aside className="side-panel">
                <PasteEditor
                  paste={selectedPaste}
                  draft={editDraft}
                  onDraft={setEditDraft}
                  onSave={saveSelectedPaste}
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
                  sharePassword={shareDraft.password}
                  locale={locale}
                />
              </aside>
            </section>
          </>
        ) : null}

        {view === "shared" ? (
          <Panel title={t("shares")} meta={`${shares.length} ${t("sharedLinks")}`}>
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
                    {share.loginRequired ? t("loginRequired") : t("anonymous")} ·
                    {t("expires")} {new Date(share.expiresAt).toLocaleString()}
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
          <Panel title={t("billing")} meta={t("stripeUsdtPayments")}>
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
              user.deleteScheduledAt ? t("deletionScheduled") : t("accountActive")
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
              <button type="button" onClick={updateProfile}>
                {t("saveProfile")}
              </button>
            </div>
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
          <Panel title={t("admin")} meta={t("auditQueuesCleanup")}>
            <div className="metric-grid">
              {Object.entries(adminStats ?? {}).map(([key, value]) => (
                <div className="metric" key={key}>
                  <span>{key}</span>
                  <strong>{String(value)}</strong>
                </div>
              ))}
            </div>
            <button type="button" onClick={runCleanup}>
              {t("runCleanup")}
            </button>
            <button type="button" onClick={runBillingReconciliation}>
              {t("runBillingReconcile")}
            </button>
            <div className="admin-grid">
              <section>
                <h3>{t("users")}</h3>
                {adminData.users.slice(0, 5).map((item) => (
                  <article className="list-card" key={item.id}>
                    <strong>{item.email}</strong>
                    <span>
                      {item.planId} · {item.frozen ? t("frozen") : t("active")}
                    </span>
                  </article>
                ))}
              </section>
              <section>
                <h3>{t("attachments")}</h3>
                {adminData.attachments.slice(0, 5).map((attachment) => (
                  <article className="list-card" key={attachment.id}>
                    <div>
                      <strong>{attachment.fileName}</strong>
                      <span>
                        {attachment.scanStatus} · {attachment.status} ·{" "}
                        {attachment.sha256.slice(0, 12)}
                      </span>
                    </div>
                    <div className="button-row compact">
                      <button
                        type="button"
                        onClick={() => void adminRetryScan(attachment.id)}
                      >
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
                        {attachment.status === "frozen" ? t("release") : t("freeze")}
                      </button>
                    </div>
                  </article>
                ))}
              </section>
              <section>
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
                      {t("revoke")}
                    </button>
                  </article>
                ))}
              </section>
              <section>
                <h3>{t("orders")}</h3>
                {adminData.orders.slice(0, 5).map((order) => {
                  const status = orderStatusDetail(order.status, locale);
                  return (
                    <article className="list-card" key={order.id}>
                      <div>
                        <strong>{order.planId}</strong>
                        <span>
                          {order.provider} ·{" "}
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
                        {t("paid")}
                      </button>
                    </article>
                  );
                })}
              </section>
              <section>
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
                  <span>{adminData.queues?.cleanupFailures.length ?? 0}</span>
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
                          {mail.to} · {mail.status} · {mail.attempts}{" "}
                          {t("attempts")}
                        </span>
                        {mail.lastError ? <span>{mail.lastError}</span> : null}
                      </div>
                    </article>
                  ))}
                {(adminData.queues?.reports ?? []).slice(0, 5).map((report) => (
                  <article className="list-card" key={report.id}>
                    <div>
                      <strong>{report.target}</strong>
                      <span>
                        {report.status} · {report.reason}
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
                        {t("resolve")}
                      </button>
                      <button
                        type="button"
                        onClick={() =>
                          void adminResolveReport(report, "dismissed")
                        }
                        disabled={report.status === "dismissed"}
                      >
                        {t("dismiss")}
                      </button>
                    </div>
                  </article>
                ))}
              </section>
              <section>
                <h3>{t("webhooks")}</h3>
                {adminData.webhookEvents.slice(0, 5).map((event) => (
                  <article className="list-card" key={event.id}>
                    <div>
                      <strong>{event.eventType}</strong>
                      <span>
                        {event.provider} · {event.targetId}
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
            </div>
            {auditLogs.slice(0, 8).map((log) => (
              <article className="list-card" key={log.id}>
                <strong>{log.action}</strong>
                <span>
                  {log.target} · {new Date(log.createdAt).toLocaleString()}
                </span>
              </article>
            ))}
          </Panel>
        ) : null}
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
  locale,
}: {
  pastes: Paste[];
  selectedId: string;
  onSelect: (id: string) => void;
  onCopy: (text: string) => void;
  onDelete: (id: string) => void;
  onExtend: (paste: Paste, expiresInSeconds: number) => void;
  onToggleFlag: (paste: Paste, field: "pinned" | "favorite") => void;
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
              aria-label={paste.pinned ? t("unpinPaste") : t("pinPaste")}
            >
              <Pin size={17} aria-hidden="true" />
            </button>
            <button
              className={`icon-button small ${paste.favorite ? "active" : ""}`}
              type="button"
              onClick={() => onToggleFlag(paste, "favorite")}
              aria-label={
                paste.favorite ? t("removeFavorite") : t("favoritePaste")
              }
            >
              <Star size={17} aria-hidden="true" />
            </button>
            <button
              className="icon-button small"
              type="button"
              onClick={() => onCopy(paste.text)}
              aria-label={t("copyText")}
            >
              <ClipboardCopy size={17} aria-hidden="true" />
            </button>
            <button
              className="icon-button small"
              type="button"
              onClick={() => onExtend(paste, 7 * 24 * 60 * 60)}
              aria-label={t("extendPaste")}
            >
              <TimerReset size={17} aria-hidden="true" />
            </button>
            <button
              className="icon-button small danger"
              type="button"
              onClick={() => onDelete(paste.id)}
              aria-label={t("deletePaste")}
            >
              <Trash2 size={17} aria-hidden="true" />
            </button>
          </div>
          {paste.shareCount ? (
            <span className="share-chip">{t("shared")}</span>
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

function LandingPage({
  locale,
  plans,
}: {
  locale: Locale;
  plans: PlanCatalog["plans"];
}) {
  const t = copyFor(locale);
  const content = landingContentFor(locale);
  const showcasePlan = plans[0];
  const priceCards =
    plans.length > 0
      ? plans.slice(0, 3)
      : [
          {
            id: "free",
            name: "Free",
            activePasteLimit: 25,
            activeStorageBytes: 256 * 1024 * 1024,
            maxRetentionSeconds: 7 * 24 * 60 * 60,
          },
          {
            id: "pro",
            name: "Pro",
            activePasteLimit: 500,
            activeStorageBytes: 10 * 1024 * 1024 * 1024,
            maxRetentionSeconds: 180 * 24 * 60 * 60,
          },
        ];

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
          <a href="#pricing">{content.navPricing}</a>
        </nav>
        <div className="landing-actions">
          <a className="landing-link-button" href="/login">
            {t("login")}
          </a>
          <a className="landing-primary-button" href="/register">
            {t("register")}
          </a>
        </div>
      </header>

      <section className="landing-hero" id="product">
        <div className="landing-copy">
          <span className="eyebrow">{content.eyebrow}</span>
          <h1>{content.title}</h1>
          <p>{content.subtitle}</p>
          <div className="landing-cta-row">
            <a className="landing-primary-button large" href="/register">
              <Sparkles size={18} aria-hidden="true" />
              {content.primaryCta}
            </a>
            <a className="landing-link-button large" href="/login">
              <KeyRound size={18} aria-hidden="true" />
              {content.secondaryCta}
            </a>
          </div>
        </div>

        <div className="landing-clipboard-card" aria-label={content.workspaceLabel}>
          <div className="clipboard-window-bar">
            <span />
            <span />
            <span />
            <strong>{content.workspaceLabel}</strong>
          </div>
          <div className="clipboard-tabs" aria-hidden="true">
            <span className="active">{t("text")}</span>
            <span>{t("files")}</span>
            <span>{t("shared")}</span>
          </div>
          <label className="clipboard-field">
            <span>{t("title")}</span>
            <input readOnly value="Launch notes, API keys, or handoff text" />
          </label>
          <label className="clipboard-field">
            <span>{t("text")}</span>
            <textarea
              readOnly
              value={
                "Paste once, open anywhere.\nSet expiry, attach files, and revoke links without leaving the clipboard."
              }
            />
          </label>
          <div className="clipboard-button-row">
            <button type="button">
              <UploadCloud size={16} aria-hidden="true" />
              {t("upload")}
            </button>
            <button type="button">
              <Link2 size={16} aria-hidden="true" />
              {t("share")}
            </button>
          </div>
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

      <section className="landing-pricing" id="pricing">
        <div className="landing-section-heading">
          <span className="eyebrow">{t("currentPlan")}</span>
          <h2>{t("stripeUsdtPayments")}</h2>
        </div>
        <div className="landing-plan-grid">
          {priceCards.map((plan) => (
            <article className="landing-plan-card" key={plan.id}>
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
            {showcasePlan.name} · {formatBytes(showcasePlan.activeStorageBytes)} ·{" "}
            {formatDuration(showcasePlan.maxRetentionSeconds)}
          </p>
        ) : null}
      </section>

      <PublicFooter locale={locale} />
    </main>
  );
}

function AuthScreen({
  mode,
  auth,
  busy,
  message,
  magicToken,
  resetToken,
  verificationToken,
  passwordResetLinkActive,
  onAuth,
  onLogin,
  onRegister,
  onGoogle,
  onStartMagic,
  onFinishMagic,
  onMagicToken,
  onPasswordReset,
  onFinishPasswordReset,
  onResetToken,
  onVerificationToken,
  onFinishVerification,
  locale,
}: {
  mode: AuthMode;
  auth: AuthFormState;
  busy: boolean;
  message: string;
  magicToken: string;
  resetToken: string;
  verificationToken: string;
  passwordResetLinkActive: boolean;
  onAuth: (value: AuthFormState) => void;
  onLogin: () => void;
  onRegister: () => void;
  onGoogle: () => void;
  onStartMagic: () => void;
  onFinishMagic: () => void;
  onMagicToken: (value: string) => void;
  onPasswordReset: () => void;
  onFinishPasswordReset: () => void;
  onResetToken: (value: string) => void;
  onVerificationToken: (value: string) => void;
  onFinishVerification: () => void;
  locale: Locale;
}) {
  const t = copyFor(locale);
  const content = landingContentFor(locale);
  const isRegister = mode === "register";

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
            } else {
              onLogin();
            }
          }}
        >
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
          <label>
            {t("password")}
            <input
              autoComplete={isRegister ? "new-password" : "current-password"}
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
          <button className="auth-submit" type="submit" disabled={busy}>
            {isRegister ? (
              <Sparkles size={16} aria-hidden="true" />
            ) : (
              <KeyRound size={16} aria-hidden="true" />
            )}
            {isRegister ? t("register") : t("login")}
          </button>
        </form>

        <button
          className="auth-oauth-button"
          type="button"
          onClick={onGoogle}
          disabled={busy}
        >
          <ShieldCheck size={16} aria-hidden="true" />
          {t("google")}
        </button>

        {passwordResetLinkActive ? (
          <div className="auth-link-callout">
            <MailCheck size={16} aria-hidden="true" />
            <span>{t("passwordResetLinkReady")}</span>
          </div>
        ) : null}

        <details className="auth-advanced">
          <summary>{t("magicLink")}</summary>
          <div className="magic-row">
            <button type="button" onClick={onStartMagic} disabled={busy}>
              {t("magicLink")}
            </button>
            <input
              value={magicToken}
              onChange={(event) => onMagicToken(event.target.value)}
              placeholder={t("magicToken")}
            />
            <button
              type="button"
              onClick={onFinishMagic}
              disabled={busy || !magicToken}
            >
              {t("useToken")}
            </button>
          </div>
          <div className="magic-row">
            <button type="button" onClick={onPasswordReset} disabled={busy}>
              {t("reset")}
            </button>
            <input
              value={resetToken}
              onChange={(event) => onResetToken(event.target.value)}
              placeholder={t("resetToken")}
            />
            <button
              type="button"
              onClick={onFinishPasswordReset}
              disabled={busy || !resetToken}
            >
              {t("updatePassword")}
            </button>
          </div>
          <div className="magic-row manual-token-row">
            <span>{t("manualTokenFallback")}</span>
            <input
              value={verificationToken}
              onChange={(event) => onVerificationToken(event.target.value)}
              placeholder={t("verificationToken")}
            />
            <button
              type="button"
              onClick={onFinishVerification}
              disabled={busy || !verificationToken}
            >
              {t("verify")}
            </button>
          </div>
        </details>

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
        <div className="magic-row">
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
              {access.paste.attachments.map((attachment) => (
                <AttachmentDownloadItem
                  attachment={attachment}
                  context="public"
                  href={sharedAttachmentDownloadPath(
                    access.share.token,
                    attachment.id,
                    password,
                  )}
                  icon="download"
                  key={attachment.id}
                  locale={locale}
                />
              ))}
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
          <a className="ghost-button" href="/">
            <ClipboardCopy size={16} aria-hidden="true" />
            {t("openApp")}
          </a>
          <a className="ghost-button" href="/support">
            <LifeBuoy size={16} aria-hidden="true" />
            {t("support")}
          </a>
        </div>
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
                <span>
                  {t("supportRequests")}
                </span>
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

function PublicFooter({
  compact = false,
  locale,
}: {
  compact?: boolean;
  locale: Locale;
}) {
  const t = copyFor(locale);
  const links = [
    { href: "/legal", label: t("legalHub") },
    { href: "/legal/terms", label: t("terms") },
    { href: "/legal/privacy", label: t("privacy") },
    { href: "/legal/refund", label: t("refund") },
    { href: "/legal/abuse", label: t("abuseDmca") },
    { href: "/legal/cookies", label: t("cookies") },
    { href: "/support", label: t("support") },
    { href: "/status", label: t("status") },
  ];
  return (
    <footer className={compact ? "public-footer compact" : "public-footer"}>
      <nav aria-label={t("legalNavigation")}>
        {links.map((link) => (
          <a href={link.href} key={link.href}>
            {link.label}
          </a>
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
  locale: Locale;
}) {
  const t = copyFor(locale);
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
          disabled={!paste}
        />
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
  sharePassword,
  locale,
}: {
  paste?: Paste;
  draft: ShareDraft;
  token: string;
  access: { paste: Paste; share: Share } | null;
  onDraft: (value: ShareDraft) => void;
  onCreate: () => void;
  onOpen: () => void;
  sharePassword: string;
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
                sharePassword,
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
  title,
  meta,
  children,
}: {
  title: string;
  meta: string;
  children: ReactNode;
}) {
  return (
    <section className="panel">
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

function searchParams(query: string, filter: string): URLSearchParams {
  const params = new URLSearchParams();
  if (query) params.set("query", query);
  if (filter) params.set("filter", filter);
  return params;
}

export default App;
