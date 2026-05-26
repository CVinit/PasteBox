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

type Locale = "en" | "zh";

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

const browserLocale: Locale =
  typeof navigator !== "undefined" &&
  navigator.language.toLowerCase().startsWith("zh")
    ? "zh"
    : "en";

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
  { href: "/legal/terms", label: "Terms" },
  { href: "/legal/privacy", label: "Privacy" },
  { href: "/legal/refund", label: "Refund" },
  { href: "/legal/abuse", label: "Abuse/DMCA" },
  { href: "/legal/cookies", label: "Cookies" },
  { href: "/status", label: "Status" },
  { href: "/support", label: "Support" },
  { href: "/legal/account-deletion", label: "Deletion" },
  { href: "/legal/data-export", label: "Export" },
  { href: "/legal/data-retention", label: "Retention" },
  { href: "/legal/subprocessors", label: "Subprocessors" },
];

function publicPageForPath(pathname: string): PublicPage | null {
  const normalized = pathname.replace(/\/+$/, "") || "/";
  return publicPages.find((page) => page.path === normalized) ?? null;
}

const copy: Record<Locale, Record<string, string>> = {
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
    stripeUsdtPayments: "Stripe and USDT payment lifecycle",
    billingSupport: "Billing support",
    billingSupportBody:
      "Refunds, duplicate charges, stuck Epusdt payments, and manual review requests are handled through the refund policy and support intake.",
    refundPolicy: "Refund Policy",
    support: "Support",
    storage: "Storage",
    file: "File",
    retention: "Retention",
    traffic: "Traffic",
    activePastes: "active pastes",
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
    deleteFailures: "Delete failures",
    resolve: "Resolve",
    dismiss: "Dismiss",
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
    passwordResetLinkReady: "Enter a new password to finish resetting your account.",
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
  },
  zh: {
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
    pastes: "条 paste",
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
    newPrivatePaste: "新建私有 paste",
    private: "私有",
    title: "标题",
    tags: "标签",
    create: "创建",
    upload: "上传",
    recentPastes: "最近 paste",
    active: "有效",
    edit: "编辑",
    noPasteSelected: "未选择 paste",
    save: "保存",
    share: "分享",
    createShare: "创建",
    open: "打开",
    report: "举报",
    revoke: "撤销",
    loginRequired: "需要登录",
    anonymous: "匿名访问",
    expires: "过期",
    stripeUsdtPayments: "Stripe 和 USDT 支付状态",
    billingSupport: "支付支持",
    billingSupportBody:
      "退款、重复扣款、卡住的 Epusdt 支付和人工审核请求会通过退款政策和支持入口处理。",
    refundPolicy: "退款政策",
    support: "支持",
    storage: "存储",
    file: "文件",
    retention: "有效期",
    traffic: "流量",
    activePastes: "条有效 paste",
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
    deleteFailures: "删除失败",
    resolve: "处理",
    dismiss: "驳回",
    webhooks: "Webhook",
    replay: "重放",
    copy: "复制",
    accountReady: "账号已创建",
    signedIn: "已登录",
    signedInWithGoogle: "已通过 Google 登录",
    verificationIssued: "验证令牌已发送",
    emailVerified: "邮箱已验证",
    emailVerifiedLogin: "邮箱已验证，请登录后继续。",
    emailVerifiedDifferentAccount: "另一个账号的邮箱已验证，切换前请先退出当前账号。",
    magicLinkIssued: "魔法链接已签发",
    signedInMagic: "已通过魔法链接登录",
    passwordResetLinkReady: "请输入新密码以完成账号密码重置。",
    signedOut: "已退出",
    allSessionsSignedOut: "所有会话已退出",
    passwordResetIssued: "密码重置已签发",
    passwordUpdated: "密码已更新",
    reportSubmitted: "举报已提交",
    pasteCreated: "Paste 已创建",
    attachmentUploaded: "附件已上传",
    shareLinkCreated: "分享链接已创建",
    shareOpened: "分享已打开",
    pasteUpdated: "Paste 已更新",
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
  },
};

function localeFor(language?: string): Locale {
  return language?.toLowerCase().startsWith("zh") ? "zh" : "en";
}

function copyFor(language?: string) {
  const locale = localeFor(language);
  return (key: string) => copy[locale][key] ?? copy.en[key] ?? key;
}

const orderStatusText: Record<
  Locale,
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
  zh: {
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
};

const attachmentScanText: Record<
  Locale,
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
  zh: {
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
      locale === "zh"
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
      locale === "zh"
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
  const riskPrefix = locale === "zh" ? "风险" : "Risk";
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
    language: "en",
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

  const activePlan = useMemo(() => {
    const planId = user?.planId ?? "free";
    return (
      quota?.plan ??
      catalog?.plans.find((plan) => plan.id === planId) ??
      catalog?.plans[0]
    );
  }, [catalog, quota, user]);
  const linkedOAuthProviders = user?.oauthProviders ?? [];

  const selectedPaste = useMemo(
    () => pastes.find((paste) => paste.id === selectedPasteId) ?? pastes[0],
    [pastes, selectedPasteId],
  );

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
        language: meResult.value.language || "en",
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
    void loadSupportContacts();
  }, [loadSupportContacts]);

  useEffect(() => {
    if (publicPage) return;
    void loadCore();
  }, [loadCore, publicPage]);

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
          language: result.user.language || "en",
        });
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
      setMessage(apiError.message || "Request failed");
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
      language: updated.language || "en",
    });
    await refreshAuthed();
  }

  async function register() {
    const result = await run(() => client.register(auth), "Account ready");
    if (result) {
      setUser(result.user);
      setVerificationToken(result.devEmailVerificationToken ?? "");
      setProfileDraft({
        displayName: result.user.displayName,
        language: result.user.language || "en",
      });
      await refreshAuthed();
    }
  }

  async function login() {
    const result = await run(
      () => client.login({ email: auth.email, password: auth.password }),
      "Signed in",
    );
    if (result) {
      setUser(result.user);
      setVerificationToken("");
      setProfileDraft({
        displayName: result.user.displayName,
        language: result.user.language || "en",
      });
      await refreshAuthed();
    }
  }

  function googleOAuth() {
    window.location.assign(
      client.googleOAuthStartPath(window.location.pathname),
    );
  }

  async function startVerification() {
    const result = await run(
      () => client.startEmailVerification(),
      "Verification issued",
    );
    if (result?.devToken) setVerificationToken(result.devToken);
  }

  async function finishVerification() {
    const updated = await run(
      () => client.finishEmailVerification(verificationToken),
      "Email verified",
    );
    if (updated) {
      await applyVerifiedEmail(updated);
    }
  }

  async function startMagic() {
    const result = await run(
      () => client.startMagic(auth.email),
      "Magic link issued",
    );
    if (result) setMagicToken(result.devToken ?? "");
  }

  async function finishMagic() {
    const result = await run(
      () => client.finishMagic(magicToken),
      "Signed in with magic link",
    );
    if (result) {
      setUser(result.user);
      setVerificationToken("");
      setProfileDraft({
        displayName: result.user.displayName,
        language: result.user.language || "en",
      });
      await refreshAuthed();
    }
  }

  async function logout() {
    await run(() => client.logout(), "Signed out");
    setUser(null);
    setPastes([]);
    setShares([]);
    setQuota(null);
  }

  async function logoutAll() {
    await run(() => client.logoutAll(), "All sessions signed out");
    setUser(null);
    setPastes([]);
    setShares([]);
    setQuota(null);
  }

  async function passwordReset() {
    const result = await run(
      () => client.passwordReset(auth.email),
      "Password reset issued",
    );
    if (result) setResetToken(result.devToken ?? "");
  }

  async function finishPasswordReset() {
    const result = await run(
      () => client.finishPasswordReset(resetToken, auth.password),
      "Password updated",
    );
    if (result) {
      setResetToken("");
      setPasswordResetLinkActive(false);
    }
  }

  async function submitReport(target = reportDraft.target) {
    const report = await run(
      () => client.report({ target, reason: reportDraft.reason || "abuse" }),
      "Report submitted",
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
      "Paste created",
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
        "Paste created",
      );
      if (!createdPaste) return;
      targetPaste = createdPaste;
      setSelectedPasteId(targetPaste.id);
    }

    const uploaded = await run(
      () => client.uploadAttachment(targetPaste.id, file),
      "Attachment uploaded",
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
      "Share link created",
    );
    if (share) {
      setShareToken(share.token);
      await refreshAuthed();
    }
  }

  async function openShare(token = shareToken) {
    const result = await run(
      () => client.accessShare(token, shareDraft.password),
      "Share opened",
    );
    if (result) setShareAccess(result);
  }

  async function openPublicShare() {
    const result = await run(
      () => client.accessShare(publicShareToken, publicSharePassword),
      "Share opened",
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
      "Paste updated",
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
      field === "pinned" ? "Pin updated" : "Favorite updated",
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
      "Expiration extended",
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
      "Order created",
    );
    if (order) await refreshAuthed();
  }

  async function exportData() {
    const payload = await run(() => client.exportMe(), "Export generated");
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
      "Deletion scheduled",
    );
    if (updated) setUser(updated);
  }

  async function cancelDelete() {
    const updated = await run(() => client.cancelDelete(), "Deletion canceled");
    if (updated) setUser(updated);
  }

  async function executeDelete() {
    const result = await run(() => client.executeDelete(), "Account deleted");
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
      "Profile updated",
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
    await run(() => client.runCleanup(), "Cleanup completed");
    await refreshAuthed();
    await refreshAdmin();
  }

  async function runBillingReconciliation() {
    await run(() => client.adminReconcileBilling(), t("billingReconciled"));
    await refreshAuthed();
    await refreshAdmin();
  }

  async function adminRetryScan(attachmentId: string) {
    await run(() => client.adminRetryScan(attachmentId), "Scan retried");
    await refreshAdmin();
  }

  async function adminFreezeAttachment(attachmentId: string, frozen: boolean) {
    await run(
      () => client.adminFreezeAttachment(attachmentId, frozen),
      frozen ? "Attachment frozen" : "Attachment released",
    );
    await refreshAdmin();
  }

  async function adminRevokeShare(shareId: string) {
    await run(() => client.adminRevokeShare(shareId), "Share revoked");
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
      "Webhook replayed",
    );
    await refreshAdmin();
  }

  async function adminResolveReport(
    report: Report,
    status: "resolved" | "dismissed",
  ) {
    await run(
      () => client.adminResolveReport(report.id, status),
      "Report updated",
    );
    await refreshAdmin();
  }

  if (publicPage) {
    return <PublicPageScreen page={publicPage} contacts={supportContacts} />;
  }

  if (!user && publicShareToken) {
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
    return (
      <main className="auth-screen">
        <section className="auth-panel">
          <div className="brand-mark">
            <div className="brand-icon">
              <ClipboardCopy size={22} aria-hidden="true" />
            </div>
            <div>
              <strong>PasteBox</strong>
              <span>Private cloud clipboard</span>
            </div>
          </div>
          <div className="auth-grid">
            <label>
              Email
              <input
                value={auth.email}
                onChange={(event) =>
                  setAuth({ ...auth, email: event.target.value })
                }
              />
            </label>
            <label>
              Password
              <input
                value={auth.password}
                type="password"
                onChange={(event) =>
                  setAuth({ ...auth, password: event.target.value })
                }
              />
            </label>
            <label>
              Display name
              <input
                value={auth.displayName}
                onChange={(event) =>
                  setAuth({ ...auth, displayName: event.target.value })
                }
              />
            </label>
            <div className="button-row">
              <button type="button" onClick={login} disabled={busy}>
                <KeyRound size={16} aria-hidden="true" />
                {t("login")}
              </button>
              <button type="button" onClick={register} disabled={busy}>
                <Sparkles size={16} aria-hidden="true" />
                {t("register")}
              </button>
              <button type="button" onClick={googleOAuth} disabled={busy}>
                <ShieldCheck size={16} aria-hidden="true" />
                {t("google")}
              </button>
            </div>
            {passwordResetLinkActive ? (
              <div className="auth-link-callout">
                <MailCheck size={16} aria-hidden="true" />
                <span>{t("passwordResetLinkReady")}</span>
              </div>
            ) : null}
            <div className="magic-row">
              <button type="button" onClick={startMagic} disabled={busy}>
                {t("magicLink")}
              </button>
              <input
                value={magicToken}
                onChange={(event) => setMagicToken(event.target.value)}
                placeholder={t("magicToken")}
              />
              <button
                type="button"
                onClick={finishMagic}
                disabled={busy || !magicToken}
              >
                {t("useToken")}
              </button>
            </div>
            <div className="magic-row">
              <button type="button" onClick={passwordReset} disabled={busy}>
                {t("reset")}
              </button>
              <input
                value={resetToken}
                onChange={(event) => setResetToken(event.target.value)}
                placeholder={t("resetToken")}
              />
              <button
                type="button"
                onClick={finishPasswordReset}
                disabled={busy || !resetToken}
              >
                {t("updatePassword")}
              </button>
            </div>
            <div className="magic-row manual-token-row">
              <span>{t("manualTokenFallback")}</span>
              <input
                value={verificationToken}
                onChange={(event) => setVerificationToken(event.target.value)}
                placeholder={t("verificationToken")}
              />
              <button
                type="button"
                onClick={finishVerification}
                disabled={busy || !verificationToken}
              >
                {t("verify")}
              </button>
            </div>
          </div>
          {message ? <p className="status-line">{message}</p> : null}
          <PublicFooter />
        </section>
      </main>
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
            Inbox
          </button>
          <button
            className={navClass(view, "shared")}
            type="button"
            onClick={() => setView("shared")}
          >
            <Link2 size={18} aria-hidden="true" />
            Shares
          </button>
          <button
            className={navClass(view, "billing")}
            type="button"
            onClick={() => setView("billing")}
          >
            <CreditCard size={18} aria-hidden="true" />
            Billing
          </button>
          <button
            className={navClass(view, "settings")}
            type="button"
            onClick={() => setView("settings")}
          >
            <UserRound size={18} aria-hidden="true" />
            Settings
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
              Admin
            </button>
          ) : null}
        </nav>

        <section className="quota-panel" aria-label="Current quota">
          <div>
            <span className="eyebrow">Current plan</span>
            <strong>{activePlan?.name ?? user.planId}</strong>
          </div>
          <div className="quota-bar">
            <span
              style={{
                width: `${quota && activePlan ? Math.min(100, (quota.activeStorageBytes / activePlan.activeStorageBytes) * 100) : 0}%`,
              }}
            />
          </div>
          <p>
            {formatBytes(quota?.activeStorageBytes ?? 0)} /{" "}
            {formatBytes(activePlan?.activeStorageBytes ?? 0)} ·{" "}
            {quota?.activePasteCount ?? 0}/{activePlan?.activePasteLimit ?? 0}{" "}
            pastes
          </p>
        </section>

        <button className="ghost-button" type="button" onClick={logout}>
          <LogOut size={16} aria-hidden="true" />
          Logout
        </button>
        <button className="ghost-button" type="button" onClick={logoutAll}>
          <LogOut size={16} aria-hidden="true" />
          Logout all
        </button>
        <PublicFooter compact />
      </aside>

      <section className="workspace">
        <header className="topbar">
          <label className="search-box">
            <Search size={18} aria-hidden="true" />
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              type="search"
              placeholder="Search"
            />
          </label>
          <label className="select-box">
            <Filter size={18} aria-hidden="true" />
            <select
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
            >
              <option value="all">All</option>
              <option value="text">Text</option>
              <option value="image">Images</option>
              <option value="file">Files</option>
              <option value="expiring">Expiring</option>
              <option value="shared">Shared</option>
              <option value="favorite">Favorites</option>
            </select>
          </label>
          {message ? <span className="status-pill">{message}</span> : null}
        </header>

        {!user.emailVerified ? (
          <section className="verify-banner">
            <div>
              <strong>Email verification required</strong>
              <span>{user.email}</span>
            </div>
            <input
              value={verificationToken}
              onChange={(event) => setVerificationToken(event.target.value)}
              placeholder="verification token"
            />
            <button type="button" onClick={startVerification} disabled={busy}>
              <Send size={16} aria-hidden="true" />
              Send
            </button>
            <button
              type="button"
              onClick={finishVerification}
              disabled={busy || !verificationToken}
            >
              <MailCheck size={16} aria-hidden="true" />
              Verify
            </button>
          </section>
        ) : null}

        {view === "inbox" ? (
          <>
            <section className="composer" aria-labelledby="new-paste-title">
              <div className="composer-heading">
                <div>
                  <span className="eyebrow">New private paste</span>
                  <h1 id="new-paste-title">PasteBox</h1>
                </div>
                <div className="privacy-badge">
                  <LockKeyhole size={16} aria-hidden="true" />
                  Private
                </div>
              </div>
              <input
                className="title-input"
                value={draft.title}
                onChange={(event) =>
                  setDraft({ ...draft, title: event.target.value })
                }
                placeholder="Title"
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
                placeholder="Text"
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
                    <option value={24 * 60 * 60}>24 hours</option>
                    <option value={7 * 24 * 60 * 60}>7 days</option>
                    <option value={30 * 24 * 60 * 60}>30 days</option>
                    <option value={180 * 24 * 60 * 60}>180 days</option>
                  </select>
                </label>
                <input
                  value={draft.tags}
                  onChange={(event) =>
                    setDraft({ ...draft, tags: event.target.value })
                  }
                  placeholder="tags"
                />
                <button type="button" onClick={createPaste} disabled={busy}>
                  <Sparkles size={16} aria-hidden="true" />
                  Create
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
                Upload
              </label>
            </section>

            <section className="content-grid">
              <PasteList
                pastes={pastes}
                selectedId={selectedPaste?.id ?? ""}
                onSelect={setSelectedPasteId}
                onCopy={(text) => void navigator.clipboard?.writeText(text)}
                onDelete={async (id) => {
                  await run(() => client.deletePaste(id), "Paste deleted");
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
          <Panel title="Shares" meta={`${shares.length} links`}>
            {shares.map((share) => (
              <article className="list-card" key={share.id}>
                <div>
                  <strong>{share.url}</strong>
                  <span>
                    {share.visitCount}/{share.maxVisits || "∞"} visits ·{" "}
                    {share.downloadCount}/{share.maxDownloads || "∞"} downloads
                  </span>
                  <span>
                    {share.loginRequired ? "login required" : "anonymous"} ·
                    expires {new Date(share.expiresAt).toLocaleString()}
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
                  Revoke
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
                  Report
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
                            void makeOrder(plan.id, price.period, option.provider)
                          }
                        >
                          {option.label} · {price.period} ·{" "}
                          {(price.amountCents / 100).toFixed(2)} {price.currency}
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
            title="Settings"
            meta={
              user.deleteScheduledAt ? "Deletion scheduled" : "Account active"
            }
          >
            <section className="notice-card">
              <FileText size={18} aria-hidden="true" />
              <div>
                <strong>Account rights</strong>
                <span>
                  Review <a href="/legal/data-export">data export</a>,{" "}
                  <a href="/legal/account-deletion">account deletion</a>,{" "}
                  <a href="/legal/privacy">privacy</a>, and{" "}
                  <a href="/support">support intake</a> before submitting
                  sensitive requests.
                </span>
              </div>
            </section>
            <section className="notice-card">
              <ShieldCheck size={18} aria-hidden="true" />
              <div>
                <strong>{t("linkedAccounts")}</strong>
                {linkedOAuthProviders.length > 0 ? (
                  <span>
                    {linkedOAuthProviders.map((provider) =>
                      provider === "google" ? t("google") : provider,
                    ).join(", ")}
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
                placeholder="Display name"
              />
              <select
                value={profileDraft.language}
                onChange={(event) =>
                  setProfileDraft({
                    ...profileDraft,
                    language: event.target.value,
                  })
                }
              >
                <option value="en">English</option>
                <option value="zh">中文</option>
              </select>
              <button type="button" onClick={updateProfile}>
                Save profile
              </button>
            </div>
            <div className="button-row">
              <button type="button" onClick={exportData}>
                <Download size={16} aria-hidden="true" />
                Export
              </button>
              <button type="button" onClick={requestDelete}>
                <Trash2 size={16} aria-hidden="true" />
                Delete request
              </button>
              <button
                type="button"
                onClick={cancelDelete}
                disabled={!user.deleteScheduledAt}
              >
                Cancel delete
              </button>
              <button
                type="button"
                onClick={executeDelete}
                disabled={!user.deleteScheduledAt}
              >
                Delete now
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
                placeholder="report target"
              />
              <input
                value={reportDraft.reason}
                onChange={(event) =>
                  setReportDraft({
                    ...reportDraft,
                    reason: event.target.value,
                  })
                }
                placeholder="report reason"
              />
              <button
                type="button"
                onClick={() => void submitReport()}
                disabled={!reportDraft.target}
              >
                <Send size={16} aria-hidden="true" />
                Report
              </button>
            </div>
          </Panel>
        ) : null}

        {view === "admin" ? (
          <Panel title="Admin" meta="Audit, queues, cleanup">
            <div className="metric-grid">
              {Object.entries(adminStats ?? {}).map(([key, value]) => (
                <div className="metric" key={key}>
                  <span>{key}</span>
                  <strong>{String(value)}</strong>
                </div>
              ))}
            </div>
            <button type="button" onClick={runCleanup}>
              Run cleanup
            </button>
            <button type="button" onClick={runBillingReconciliation}>
              {t("runBillingReconcile")}
            </button>
            <div className="admin-grid">
              <section>
                <h3>Users</h3>
                {adminData.users.slice(0, 5).map((item) => (
                  <article className="list-card" key={item.id}>
                    <strong>{item.email}</strong>
                    <span>
                      {item.planId} · {item.frozen ? "frozen" : "active"}
                    </span>
                  </article>
                ))}
              </section>
              <section>
                <h3>Attachments</h3>
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
                        Retry
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
                        {attachment.status === "frozen" ? "Release" : "Freeze"}
                      </button>
                    </div>
                  </article>
                ))}
              </section>
              <section>
                <h3>Shares</h3>
                {adminData.shares.slice(0, 5).map((share) => (
                  <article className="list-card" key={share.id}>
                    <div>
                      <strong>{share.id}</strong>
                      <span>
                        {share.visitCount} visits · {share.downloadCount}{" "}
                        downloads
                      </span>
                    </div>
                    <button
                      type="button"
                      onClick={() => void adminRevokeShare(share.id)}
                      disabled={Boolean(share.revokedAt)}
                    >
                      Revoke
                    </button>
                  </article>
                ))}
              </section>
              <section>
                <h3>Orders</h3>
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
                <h3>Queues</h3>
                <article className="list-card">
                  <strong>Scan failures</strong>
                  <span>{adminData.queues?.scanFailures.length ?? 0}</span>
                </article>
                <article className="list-card">
                  <strong>Cleanup jobs</strong>
                  <span>{adminData.queues?.cleanupJobs.length ?? 0}</span>
                </article>
                <article className="list-card">
                  <strong>Cleanup failures</strong>
                  <span>{adminData.queues?.cleanupFailures.length ?? 0}</span>
                </article>
                <article className="list-card">
                  <strong>Failed jobs</strong>
                  <span>{adminData.queues?.failedJobs.length ?? 0}</span>
                </article>
                <article className="list-card">
                  <strong>Queued mails</strong>
                  <span>{adminData.queues?.queuedMails.length ?? 0}</span>
                </article>
                <article className="list-card">
                  <strong>Failed mails</strong>
                  <span>{adminData.queues?.failedMails.length ?? 0}</span>
                </article>
                {(adminData.queues?.failedMails ?? []).slice(0, 5).map((mail) => (
                  <article className="list-card" key={mail.id}>
                    <div>
                      <strong>{mail.subject}</strong>
                      <span>
                        {mail.to} · {mail.status} · {mail.attempts} attempts
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
                        Resolve
                      </button>
                      <button
                        type="button"
                        onClick={() =>
                          void adminResolveReport(report, "dismissed")
                        }
                        disabled={report.status === "dismissed"}
                      >
                        Dismiss
                      </button>
                    </div>
                  </article>
                ))}
              </section>
              <section>
                <h3>Webhooks</h3>
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
                      Replay
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
  return (
    <section className="paste-list">
      <div className="section-heading">
        <h2>Recent pastes</h2>
        <span>{pastes.length} active</span>
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
            <h3>{paste.title || "Untitled paste"}</h3>
            <p>
              {paste.textPreview || `${paste.attachments.length} attachments`}
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
              aria-label={paste.pinned ? "Unpin paste" : "Pin paste"}
            >
              <Pin size={17} aria-hidden="true" />
            </button>
            <button
              className={`icon-button small ${paste.favorite ? "active" : ""}`}
              type="button"
              onClick={() => onToggleFlag(paste, "favorite")}
              aria-label={paste.favorite ? "Remove favorite" : "Favorite paste"}
            >
              <Star size={17} aria-hidden="true" />
            </button>
            <button
              className="icon-button small"
              type="button"
              onClick={() => onCopy(paste.text)}
              aria-label="Copy text"
            >
              <ClipboardCopy size={17} aria-hidden="true" />
            </button>
            <button
              className="icon-button small"
              type="button"
              onClick={() => onExtend(paste, 7 * 24 * 60 * 60)}
              aria-label="Extend paste"
            >
              <TimerReset size={17} aria-hidden="true" />
            </button>
            <button
              className="icon-button small danger"
              type="button"
              onClick={() => onDelete(paste.id)}
              aria-label="Delete paste"
            >
              <Trash2 size={17} aria-hidden="true" />
            </button>
          </div>
          {paste.shareCount ? <span className="share-chip">Shared</span> : null}
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
  return (
    <main className="auth-screen public-share-screen">
      <section className="auth-panel">
        <div className="brand-mark">
          <div className="brand-icon">
            <Link2 size={22} aria-hidden="true" />
          </div>
          <div>
            <strong>PasteBox</strong>
            <span>Shared paste</span>
          </div>
        </div>
        <div className="magic-row">
          <input
            value={token}
            onChange={(event) => onToken(event.target.value)}
            placeholder="share token"
          />
          <input
            value={password}
            onChange={(event) => onPassword(event.target.value)}
            placeholder="password"
            type="password"
          />
          <button type="button" onClick={onOpen} disabled={busy || !token}>
            Open
          </button>
        </div>
        {access ? (
          <section className="shared-document">
            <div className="section-heading">
              <div>
                <h1>{access.paste.title || "Shared paste"}</h1>
                <span>
                  {access.share.visitCount}/{access.share.maxVisits || "∞"}{" "}
                  visits · {formatDuration(access.paste.secondsToLive)}
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
                Copy
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
        <PublicFooter />
      </section>
    </main>
  );
}

function PublicPageScreen({
  page,
  contacts,
}: {
  page: PublicPage;
  contacts: SupportContacts | null;
}) {
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
            Open app
          </a>
          <a className="ghost-button" href="/support">
            <LifeBuoy size={16} aria-hidden="true" />
            Support
          </a>
        </div>
      </header>

      <section className="public-layout">
        <aside className="public-sidebar" aria-label="Legal navigation">
          <strong>Launch documents</strong>
          <nav>
            <a className={page.path === "/legal" ? "active" : ""} href="/legal">
              Legal hub
            </a>
            {publicLinks.map((link) => (
              <a
                className={page.path === link.href ? "active" : ""}
                href={link.href}
                key={link.href}
              >
                {link.label}
              </a>
            ))}
          </nav>
        </aside>

        <article className="public-doc">
          <div className="public-doc-meta">
            <Megaphone size={16} aria-hidden="true" />
            <span>Last updated {page.updated}</span>
          </div>
          {page.path === "/support" ? (
            <section className="support-contact-card">
              <div>
                <span>
                  Account, billing, privacy, DPA, and data-subject requests
                </span>
                {contacts?.supportEmail ? (
                  <a href={`mailto:${contacts.supportEmail}`}>
                    {contacts.supportEmail}
                  </a>
                ) : (
                  <strong>Support contact is loading</strong>
                )}
              </div>
              <div>
                <span>Abuse, malware, DMCA, and urgent takedown requests</span>
                {contacts?.abuseEmail ? (
                  <a href={`mailto:${contacts.abuseEmail}`}>
                    {contacts.abuseEmail}
                  </a>
                ) : (
                  <strong>Abuse contact is loading</strong>
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
            <span>
              This page reflects the confirmed public-beta launch architecture
              and must be updated when providers, retention, or request
              workflows change.
            </span>
          </footer>
        </article>
      </section>
    </main>
  );
}

function PublicFooter({ compact = false }: { compact?: boolean }) {
  return (
    <footer className={compact ? "public-footer compact" : "public-footer"}>
      <a href="/legal">Legal hub</a>
      <a href="/legal/terms">Terms</a>
      <a href="/legal/privacy">Privacy</a>
      <a href="/legal/refund">Refund</a>
      <a href="/legal/abuse">Abuse/DMCA</a>
      <a href="/legal/cookies">Cookies</a>
      <a href="/support">Support</a>
      <a href="/status">Status</a>
    </footer>
  );
}

function PasteEditor({
  paste,
  draft,
  onDraft,
  onSave,
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
}) {
  return (
    <Panel
      title="Edit"
      meta={paste ? paste.title || paste.id : "No paste selected"}
    >
      <div className="form-grid single">
        <input
          value={draft.title}
          onChange={(event) => onDraft({ ...draft, title: event.target.value })}
          placeholder="title"
          disabled={!paste}
        />
        <textarea
          value={draft.text}
          onChange={(event) => onDraft({ ...draft, text: event.target.value })}
          placeholder="text"
          disabled={!paste}
        />
        <input
          value={draft.tags}
          onChange={(event) => onDraft({ ...draft, tags: event.target.value })}
          placeholder="tags"
          disabled={!paste}
        />
      </div>
      <button type="button" onClick={onSave} disabled={!paste}>
        Save
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
  return (
    <Panel
      title="Share"
      meta={paste ? paste.title || paste.id : "No paste selected"}
    >
      <div className="form-grid single">
        <input
          value={draft.password}
          onChange={(event) =>
            onDraft({ ...draft, password: event.target.value })
          }
          placeholder="password"
        />
        <label className="check-row">
          <input
            type="checkbox"
            checked={draft.loginRequired}
            onChange={(event) =>
              onDraft({ ...draft, loginRequired: event.target.checked })
            }
          />
          Login required
        </label>
        <input
          value={draft.maxVisits}
          min={0}
          type="number"
          onChange={(event) =>
            onDraft({ ...draft, maxVisits: Number(event.target.value) })
          }
          placeholder="max visits"
        />
        <input
          value={draft.maxDownloads}
          min={0}
          type="number"
          onChange={(event) =>
            onDraft({ ...draft, maxDownloads: Number(event.target.value) })
          }
          placeholder="max downloads"
        />
        <select
          value={draft.expiresInSeconds}
          onChange={(event) =>
            onDraft({ ...draft, expiresInSeconds: Number(event.target.value) })
          }
        >
          <option value={24 * 60 * 60}>24 hours</option>
          <option value={7 * 24 * 60 * 60}>7 days</option>
          <option value={30 * 24 * 60 * 60}>30 days</option>
        </select>
      </div>
      <div className="button-row">
        <button type="button" onClick={onCreate} disabled={!paste}>
          <Link2 size={16} aria-hidden="true" />
          Create
        </button>
        <button type="button" onClick={onOpen} disabled={!token}>
          Open
        </button>
      </div>
      {token ? <code>{token}</code> : null}
      {access ? (
        <div className="share-preview">
          <article className="list-card">
            <div>
              <strong>{access.paste.title || "Shared paste"}</strong>
              <span>
                {access.share.visitCount} visits · {access.share.downloadCount}{" "}
                downloads
              </span>
              <span>
                {access.paste.textPreview ||
                  `${access.paste.attachments.length} attachments`}
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
