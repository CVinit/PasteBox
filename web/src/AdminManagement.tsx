import { useEffect, useState, type ReactNode } from "react";
import {
  Ban,
  Check,
  ChevronLeft,
  ChevronRight,
  FileUp,
  Link2,
  RotateCcw,
  Search,
  Snowflake,
  Undo2,
  Users,
  X,
} from "lucide-react";
import {
  client,
  formatBytes,
  type AdminAttachment,
  type AdminShare,
  type AuditLog,
  type Plan,
  type User,
} from "./api";

type Translate = (key: string) => string;
type Locale = "en" | "zh-CN" | "zh-TW" | "es";

export const adminCopy: Record<Locale, Record<string, string>> = {
  en: {
    adminTabUsers: "User management",
    adminTabFiles: "File management",
    adminTabShares: "Share management",
    adminTabOrders: "Orders",
    adminTabAudit: "Audit log",
    adminTabQueues: "Queues & cleanup",
    adminOverviewDescription:
      "Service activity, resource usage and pending work.",
    adminUsersDescription:
      "Find accounts, manage access and adjust membership.",
    adminFilesDescription:
      "Review uploaded files, scan results and content access.",
    adminSharesDescription: "Review shared links and revoke access.",
    adminPlansDescription:
      "Control public pricing, plan limits and purchase options.",
    adminAuditDescription: "Recent account and administrative activity.",
    adminQueuesDescription: "Background jobs, reports and cleanup.",
    adminMenu: "Admin navigation",
    adminConfigurationDescription:
      "Manage site settings and service connections.",
    adminSecurityDescription: "Registration, verification and rate limits.",
    adminGuestDescription: "Guest upload access, capacity and retention.",
    adminServicesDescription:
      "Service connections, runtime settings and alerts.",
    adminOrdersDescription:
      "Review orders, manual payments and reconciliation.",
    auditLogin: "Signed in with an external provider",

    pricingVisibility: "Show pricing on the site",
    pricingVisibilityDescription:
      "Display pricing on the public site and the membership entry in the workspace. Existing memberships remain active.",
    pricingVisibilitySaved: "Pricing visibility saved",
    adminUserSearch: "Email, name or user ID",
    adminFileSearch: "Filename, user ID, SHA256 or status",
    adminSearch: "Search",
    adminRefresh: "Refresh",
    adminLoading: "Loading…",
    adminEmpty: "No matching records",
    adminLoadFailed: "Could not load records",
    adminPrevious: "Previous",
    adminNext: "Next",
    adminPage: "Page",
    adminPagination: "List pages",
    adminActions: "Actions",
    adminOwner: "Owner",
    adminStatus: "Status",
    adminContentStatus: "Content status",
    adminTakenDown: "Taken down",
    adminShareRevoked: "Link revoked",
    adminShareExpired: "Expired",
    adminCreatedAt: "Created",
    adminExpiresAt: "Expires",
    adminNeverExpires: "No expiry",
    adminPlan: "Plan",
    adminRole: "Role",
    adminRoleAdmin: "Administrator",
    adminRoleUser: "User",
    adminAccount: "Account",
    adminEditPlan: "Change plan",
    adminPlanReason: "Reason for change",
    adminSave: "Save",
    adminCancel: "Cancel",
    adminSaved: "Changes saved",
    adminSelfFreeze: "You cannot freeze your own account here",
    adminTakedown: "Take down content",
    adminTakedownConfirm:
      "Take down this content and all its files? Existing share links will stop working.",
    adminFileTakenDown: "Content taken down",
    adminAuditTime: "Time",
    adminAuditAction: "Action",
    adminAuditActor: "Actor",
    adminAuditTarget: "Target",
    adminViewDetails: "View full identifiers",
    adminNoAudit: "No audit activity yet",
    adminAuditSystem: "System",
    adminPriceVisible: "Show this price",
    adminPricePurchasable: "Allow purchase",
    auditCatalog: "Updated plans and prices",
    auditRuntime: "Updated runtime settings",
    auditManaged: "Updated application settings",
    auditUserPlan: "Changed user plan",
    auditUserFreeze: "Changed account access",
    auditFileFreeze: "Changed file access",
    auditScan: "Retried file scan",
    auditTakedown: "Took down content",
    auditShare: "Revoked share",
    auditOAuthLinked: "Linked sign-in provider",
    auditOAuthUnlinked: "Unlinked sign-in provider",
    auditReport: "Processed report",
    auditWebhook: "Replayed payment callback",
    auditRedemptionCreate: "Created redemption batch",
    auditRedemptionUpdate: "Updated redemption batch",
    auditProvider: "Tested service connection",
    auditAlert: "Sent test alert",
  },
  "zh-CN": {
    adminTabUsers: "用户管理",
    adminTabFiles: "文件管理",
    adminTabShares: "分享管理",
    adminTabOrders: "订单管理",
    adminTabAudit: "审计日志",
    adminTabQueues: "队列与清理",
    adminOverviewDescription: "查看服务运行情况、资源用量和待处理事项。",
    adminUsersDescription: "查找用户，管理账号状态和套餐权益。",
    adminFilesDescription: "查看上传文件、扫描结果，管理内容访问。",
    adminSharesDescription: "查看分享记录，撤销分享链接。",
    adminPlansDescription: "设置前台套餐展示、套餐额度和购买选项。",
    adminAuditDescription: "查看最近的账号活动和管理操作。",
    adminQueuesDescription: "查看后台任务、处理举报和执行清理。",
    adminMenu: "后台导航",
    adminConfigurationDescription: "管理站点设置和外部服务连接。",
    adminSecurityDescription: "设置注册验证和请求频率限制。",
    adminGuestDescription: "设置游客上传权限、容量和保留时间。",
    adminServicesDescription: "管理服务连接、运行配置和告警。",
    adminOrdersDescription: "查看订单、人工入账和支付对账。",
    auditLogin: "通过外部账号登录",

    pricingVisibility: "在前台显示套餐",
    pricingVisibilityDescription:
      "控制官网套餐区和工作区会员入口的展示，关闭后已购套餐权益仍然有效。",
    pricingVisibilitySaved: "套餐展示设置已保存",
    adminUserSearch: "搜索邮箱、昵称或用户 ID",
    adminFileSearch: "搜索文件名、用户 ID、SHA256 或状态",
    adminSearch: "搜索",
    adminRefresh: "刷新",
    adminLoading: "加载中…",
    adminEmpty: "没有匹配的记录",
    adminLoadFailed: "记录加载失败",
    adminPrevious: "上一页",
    adminNext: "下一页",
    adminPage: "页码",
    adminPagination: "列表分页",
    adminActions: "操作",
    adminOwner: "归属用户",
    adminStatus: "状态",
    adminContentStatus: "内容状态",
    adminTakenDown: "已下架",
    adminShareRevoked: "链接已撤销",
    adminShareExpired: "已过期",
    adminCreatedAt: "创建时间",
    adminExpiresAt: "到期时间",
    adminNeverExpires: "长期有效",
    adminPlan: "套餐",
    adminRole: "角色",
    adminRoleAdmin: "管理员",
    adminRoleUser: "普通用户",
    adminAccount: "账号",
    adminEditPlan: "调整套餐",
    adminPlanReason: "调整原因",
    adminSave: "保存",
    adminCancel: "取消",
    adminSaved: "修改已保存",
    adminSelfFreeze: "不能在这里冻结当前登录账号",
    adminTakedown: "下架内容",
    adminTakedownConfirm:
      "确定下架这条内容及其全部文件？已有分享链接将无法访问。",
    adminFileTakenDown: "内容已下架",
    adminAuditTime: "时间",
    adminAuditAction: "操作",
    adminAuditActor: "操作人",
    adminAuditTarget: "操作对象",
    adminViewDetails: "查看完整标识",
    adminNoAudit: "暂无审计记录",
    adminAuditSystem: "系统",
    adminPriceVisible: "展示此价格",
    adminPricePurchasable: "允许购买",
    auditCatalog: "更新套餐与价格",
    auditRuntime: "更新运行配置",
    auditManaged: "更新应用配置",
    auditUserPlan: "调整用户套餐",
    auditUserFreeze: "调整账号冻结状态",
    auditFileFreeze: "调整文件冻结状态",
    auditScan: "重新扫描文件",
    auditTakedown: "下架内容",
    auditShare: "撤销分享",
    auditOAuthLinked: "绑定登录方式",
    auditOAuthUnlinked: "解绑登录方式",
    auditReport: "处理举报",
    auditWebhook: "重放支付回调",
    auditRedemptionCreate: "创建兑换码批次",
    auditRedemptionUpdate: "更新兑换码批次",
    auditProvider: "测试服务连接",
    auditAlert: "发送测试告警",
  },
  "zh-TW": {
    adminTabUsers: "使用者管理",
    adminTabFiles: "檔案管理",
    adminTabShares: "分享管理",
    adminTabOrders: "訂單管理",
    adminTabAudit: "稽核紀錄",
    adminTabQueues: "佇列與清理",
    adminOverviewDescription: "查看服務執行情況、資源用量及待處理事項。",
    adminUsersDescription: "尋找使用者，管理帳號狀態及方案權益。",
    adminFilesDescription: "查看上傳檔案、掃描結果及管理內容存取。",
    adminSharesDescription: "查看分享紀錄，撤銷分享連結。",
    adminPlansDescription: "設定前台方案展示、方案額度及購買選項。",
    adminAuditDescription: "查看最近的帳號活動及管理操作。",
    adminQueuesDescription: "查看背景工作、處理檢舉及執行清理。",
    adminMenu: "後台導覽",
    adminConfigurationDescription: "管理網站設定及外部服務連線。",
    adminSecurityDescription: "設定註冊驗證及請求頻率限制。",
    adminGuestDescription: "設定訪客上傳權限、容量及保留時間。",
    adminServicesDescription: "管理服務連線、執行設定及告警。",
    adminOrdersDescription: "查看訂單、人工入帳及付款對帳。",
    auditLogin: "透過外部帳號登入",

    pricingVisibility: "在前台顯示方案",
    pricingVisibilityDescription:
      "控制官網方案區及工作區會員入口的展示，關閉後已購方案權益仍然有效。",
    pricingVisibilitySaved: "方案展示設定已儲存",
    adminUserSearch: "搜尋電子郵件、暱稱或使用者 ID",
    adminFileSearch: "搜尋檔名、使用者 ID、SHA256 或狀態",
    adminSearch: "搜尋",
    adminRefresh: "重新整理",
    adminLoading: "載入中…",
    adminEmpty: "沒有符合的紀錄",
    adminLoadFailed: "紀錄載入失敗",
    adminPrevious: "上一頁",
    adminNext: "下一頁",
    adminPage: "頁碼",
    adminPagination: "清單分頁",
    adminActions: "操作",
    adminOwner: "所屬使用者",
    adminStatus: "狀態",
    adminContentStatus: "內容狀態",
    adminTakenDown: "已下架",
    adminShareRevoked: "連結已撤銷",
    adminShareExpired: "已過期",
    adminCreatedAt: "建立時間",
    adminExpiresAt: "到期時間",
    adminNeverExpires: "長期有效",
    adminPlan: "方案",
    adminRole: "角色",
    adminRoleAdmin: "管理員",
    adminRoleUser: "一般使用者",
    adminAccount: "帳號",
    adminEditPlan: "調整方案",
    adminPlanReason: "調整原因",
    adminSave: "儲存",
    adminCancel: "取消",
    adminSaved: "修改已儲存",
    adminSelfFreeze: "無法在此凍結目前登入帳號",
    adminTakedown: "下架內容",
    adminTakedownConfirm:
      "確定下架這筆內容及其所有檔案？現有分享連結將無法存取。",
    adminFileTakenDown: "內容已下架",
    adminAuditTime: "時間",
    adminAuditAction: "操作",
    adminAuditActor: "操作人",
    adminAuditTarget: "操作對象",
    adminViewDetails: "查看完整識別碼",
    adminNoAudit: "暫無稽核紀錄",
    adminAuditSystem: "系統",
    adminPriceVisible: "展示此價格",
    adminPricePurchasable: "允許購買",
    auditCatalog: "更新方案與價格",
    auditRuntime: "更新執行設定",
    auditManaged: "更新應用設定",
    auditUserPlan: "調整使用者方案",
    auditUserFreeze: "調整帳號凍結狀態",
    auditFileFreeze: "調整檔案凍結狀態",
    auditScan: "重新掃描檔案",
    auditTakedown: "下架內容",
    auditShare: "撤銷分享",
    auditOAuthLinked: "綁定登入方式",
    auditOAuthUnlinked: "解除登入綁定",
    auditReport: "處理檢舉",
    auditWebhook: "重播付款回呼",
    auditRedemptionCreate: "建立兌換碼批次",
    auditRedemptionUpdate: "更新兌換碼批次",
    auditProvider: "測試服務連線",
    auditAlert: "傳送測試告警",
  },
  es: {
    adminTabUsers: "Usuarios",
    adminTabFiles: "Archivos",
    adminTabShares: "Enlaces",
    adminTabOrders: "Pedidos",
    adminTabAudit: "Auditoría",
    adminTabQueues: "Colas y limpieza",
    adminOverviewDescription: "Actividad, recursos y tareas pendientes.",
    adminUsersDescription:
      "Busca cuentas, gestiona el acceso y cambia los planes.",
    adminFilesDescription: "Revisa archivos, análisis y acceso al contenido.",
    adminSharesDescription:
      "Revisa los enlaces compartidos y revoca el acceso.",
    adminPlansDescription:
      "Configura los planes públicos, límites y opciones de compra.",
    adminAuditDescription: "Actividad reciente de cuentas y administración.",
    adminQueuesDescription: "Tareas en segundo plano, reportes y limpieza.",
    adminMenu: "Navegación de administración",
    adminConfigurationDescription:
      "Configuración del sitio y conexiones de servicios.",
    adminSecurityDescription:
      "Registro, verificación y límites de solicitudes.",
    adminGuestDescription: "Permisos, capacidad y retención para invitados.",
    adminServicesDescription:
      "Conexiones, configuración y alertas de servicios.",
    adminOrdersDescription: "Pedidos, pagos manuales y conciliación.",
    auditLogin: "Acceso con proveedor externo",

    pricingVisibility: "Mostrar planes en el sitio",
    pricingVisibilityDescription:
      "Muestra los planes públicos y la sección de membresía. Las membresías existentes permanecen activas.",
    pricingVisibilitySaved: "Visibilidad de planes guardada",
    adminUserSearch: "Correo, nombre o ID de usuario",
    adminFileSearch: "Nombre de archivo, ID, SHA256 o estado",
    adminSearch: "Buscar",
    adminRefresh: "Actualizar",
    adminLoading: "Cargando…",
    adminEmpty: "No hay registros coincidentes",
    adminLoadFailed: "No se pudieron cargar los registros",
    adminPrevious: "Anterior",
    adminNext: "Siguiente",
    adminPage: "Página",
    adminPagination: "Páginas de la lista",
    adminActions: "Acciones",
    adminOwner: "Propietario",
    adminStatus: "Estado",
    adminContentStatus: "Estado del contenido",
    adminTakenDown: "Retirado",
    adminShareRevoked: "Enlace revocado",
    adminShareExpired: "Vencido",
    adminCreatedAt: "Creado",
    adminExpiresAt: "Vencimiento",
    adminNeverExpires: "Sin vencimiento",
    adminPlan: "Plan",
    adminRole: "Rol",
    adminRoleAdmin: "Administrador",
    adminRoleUser: "Usuario",
    adminAccount: "Cuenta",
    adminEditPlan: "Cambiar plan",
    adminPlanReason: "Motivo del cambio",
    adminSave: "Guardar",
    adminCancel: "Cancelar",
    adminSaved: "Cambios guardados",
    adminSelfFreeze: "No puedes congelar tu propia cuenta aquí",
    adminTakedown: "Retirar contenido",
    adminTakedownConfirm:
      "¿Retirar este contenido y todos sus archivos? Los enlaces compartidos dejarán de funcionar.",
    adminFileTakenDown: "Contenido retirado",
    adminAuditTime: "Fecha",
    adminAuditAction: "Acción",
    adminAuditActor: "Autor",
    adminAuditTarget: "Destino",
    adminViewDetails: "Ver identificadores completos",
    adminNoAudit: "Sin actividad de auditoría",
    adminAuditSystem: "Sistema",
    adminPriceVisible: "Mostrar este precio",
    adminPricePurchasable: "Permitir compras",
    auditCatalog: "Planes y precios actualizados",
    auditRuntime: "Configuración de ejecución actualizada",
    auditManaged: "Configuración de aplicación actualizada",
    auditUserPlan: "Plan de usuario cambiado",
    auditUserFreeze: "Acceso de cuenta modificado",
    auditFileFreeze: "Acceso de archivo modificado",
    auditScan: "Análisis reintentado",
    auditTakedown: "Contenido retirado",
    auditShare: "Enlace revocado",
    auditOAuthLinked: "Proveedor de acceso vinculado",
    auditOAuthUnlinked: "Proveedor desvinculado",
    auditReport: "Reporte procesado",
    auditWebhook: "Callback de pago reintentado",
    auditRedemptionCreate: "Lote de canje creado",
    auditRedemptionUpdate: "Lote de canje actualizado",
    auditProvider: "Conexión probada",
    auditAlert: "Alerta de prueba enviada",
  },
};

const pageSize = 20;
type PageLoader<T> = (
  query: string,
  offset: number,
  signal: AbortSignal,
) => Promise<T[]>;
const loadUsers: PageLoader<User> = async (query, offset, signal) =>
  (await client.adminUsers(query, pageSize + 1, offset, signal)).users;
const loadFiles: PageLoader<AdminAttachment> = async (query, offset, signal) =>
  (await client.adminAttachments(query, pageSize + 1, offset, signal))
    .attachments;
const loadShares: PageLoader<AdminShare> = async (_query, offset, signal) =>
  (await client.adminShares(pageSize + 1, offset, signal)).shares;

// Search is sent to the server before pagination. Aborted requests cannot replace
// a newer query's rows or show stale failures after leaving this page.
function useAdminPage<T>(load: PageLoader<T>) {
  const [request, setRequest] = useState({ query: "", page: 0, revision: 0 });
  const [rows, setRows] = useState<T[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    setRows([]);
    void load(request.query, request.page * pageSize, controller.signal)
      .then((result) => {
        if (!controller.signal.aborted) setRows(result);
      })
      .catch((cause: unknown) => {
        if (!controller.signal.aborted)
          setError(cause instanceof Error ? cause.message : "adminLoadFailed");
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [load, request]);
  return {
    rows: rows.slice(0, pageSize),
    loading,
    error,
    page: request.page,
    hasNext: rows.length > pageSize,
    search: (query: string) =>
      setRequest((previous) => ({
        ...previous,
        query: query.trim(),
        page: 0,
        revision: previous.revision + 1,
      })),
    go: (page: number) => setRequest((previous) => ({ ...previous, page })),
    reload: () =>
      setRequest((previous) => ({
        ...previous,
        revision: previous.revision + 1,
      })),
  };
}

function useAdminAction(
  onChanged: () => void,
  reload: () => void,
  t: Translate,
) {
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState<{
    text: string;
    error: boolean;
  } | null>(null);
  async function perform(
    action: () => Promise<unknown>,
    success = t("adminSaved"),
  ) {
    setBusy(true);
    setFeedback(null);
    try {
      await action();
      setFeedback({ text: success, error: false });
      reload();
      onChanged();
      return true;
    } catch (cause) {
      setFeedback({
        text: cause instanceof Error ? cause.message : t("requestFailed"),
        error: true,
      });
      return false;
    } finally {
      setBusy(false);
    }
  }
  return {
    busy,
    perform,
    feedback: feedback ? (
      <p
        className={`admin-feedback${feedback.error ? " admin-feedback--error" : ""}`}
        role={feedback.error ? "alert" : "status"}
      >
        {feedback.text}
      </p>
    ) : null,
  };
}

function PageControls({
  page,
  hasNext,
  busy,
  onPage,
  t,
}: {
  page: number;
  hasNext: boolean;
  busy: boolean;
  onPage: (page: number) => void;
  t: Translate;
}) {
  return (
    <nav className="admin-pagination" aria-label={t("adminPagination")}>
      <span>
        {t("adminPage")} {page + 1}
      </span>
      <div className="button-row compact">
        <button
          type="button"
          disabled={busy || page === 0}
          onClick={() => onPage(page - 1)}
        >
          <ChevronLeft size={16} aria-hidden="true" />
          {t("adminPrevious")}
        </button>
        <button
          type="button"
          disabled={busy || !hasNext}
          onClick={() => onPage(page + 1)}
        >
          {t("adminNext")}
          <ChevronRight size={16} aria-hidden="true" />
        </button>
      </div>
    </nav>
  );
}

function ListToolbar({
  placeholder,
  busy,
  onSearch,
  onRefresh,
  t,
}: {
  placeholder?: string;
  busy: boolean;
  onSearch?: (query: string) => void;
  onRefresh: () => void;
  t: Translate;
}) {
  const [draft, setDraft] = useState("");
  return (
    <div className="admin-list-toolbar">
      {onSearch ? (
        <form
          className="admin-search"
          onSubmit={(event) => {
            event.preventDefault();
            onSearch(draft);
          }}
        >
          <Search size={18} aria-hidden="true" />
          <input
            type="search"
            aria-label={placeholder}
            placeholder={placeholder}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
          />
          <button type="submit" disabled={busy}>
            {t("adminSearch")}
          </button>
        </form>
      ) : (
        <span />
      )}
      <button type="button" disabled={busy} onClick={onRefresh}>
        <RotateCcw size={16} aria-hidden="true" />
        {t("adminRefresh")}
      </button>
    </div>
  );
}

function ListState({
  loading,
  error,
  empty,
  t,
}: {
  loading: boolean;
  error: string;
  empty: boolean;
  t: Translate;
}) {
  if (error)
    return (
      <p className="admin-feedback admin-feedback--error" role="alert">
        {t(error)}
      </p>
    );
  return loading || empty ? (
    <div className="admin-empty" role="status">
      {loading ? t("adminLoading") : t("adminEmpty")}
    </div>
  ) : null;
}

function localDateTime(value?: string) {
  if (!value) return "";
  const date = new Date(value);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16);
}
function dateLabel(
  value: string | undefined,
  locale: string,
  fallback: string,
) {
  return value ? new Date(value).toLocaleString(locale) : fallback;
}

export function AdminUserManager({
  plans,
  currentUserId,
  locale,
  t,
  onChanged,
}: {
  plans: Plan[];
  currentUserId: string;
  locale: Locale;
  t: Translate;
  onChanged: () => void;
}) {
  const list = useAdminPage(loadUsers);
  const action = useAdminAction(onChanged, list.reload, t);
  const [editing, setEditing] = useState<{
    id: string;
    planId: string;
    expiresAt: string;
    reason: string;
  } | null>(null);
  const locked = list.loading || action.busy;
  return (
    <section
      className="admin-section admin-manager"
      aria-label={t("adminTabUsers")}
    >
      <ListToolbar
        placeholder={t("adminUserSearch")}
        busy={locked}
        onSearch={(query) => {
          setEditing(null);
          list.search(query);
        }}
        onRefresh={list.reload}
        t={t}
      />
      {action.feedback}
      <ListState
        loading={list.loading}
        error={list.error}
        empty={list.rows.length === 0}
        t={t}
      />
      <div className="admin-record-list">
        {list.rows.map((item) => (
          <article className="admin-record" key={item.id}>
            <div className="admin-record-heading">
              <div className="admin-record-title">
                <Users size={18} aria-hidden="true" />
                <div>
                  <strong>{item.displayName || item.email}</strong>
                  <span>{item.email}</span>
                  <code title={item.id}>{item.id}</code>
                </div>
              </div>
              <span
                className={`admin-status admin-status--${item.frozen ? "danger" : "success"}`}
              >
                {item.frozen ? t("frozen") : t("active")}
              </span>
            </div>
            <dl className="admin-record-meta">
              <div>
                <dt>{t("adminPlan")}</dt>
                <dd>
                  {plans.find((plan) => plan.id === item.planId)?.name ??
                    item.planId}
                </dd>
              </div>
              <div>
                <dt>{t("adminExpiresAt")}</dt>
                <dd>
                  {dateLabel(
                    item.planExpiresAt,
                    locale,
                    t("adminNeverExpires"),
                  )}
                </dd>
              </div>
              <div>
                <dt>{t("adminRole")}</dt>
                <dd>
                  {t(
                    item.role === "admin" ? "adminRoleAdmin" : "adminRoleUser",
                  )}
                </dd>
              </div>
              <div>
                <dt>{t("adminCreatedAt")}</dt>
                <dd>{dateLabel(item.createdAt, locale, "—")}</dd>
              </div>
            </dl>
            <div className="button-row compact admin-record-actions">
              <button
                type="button"
                disabled={locked}
                onClick={() =>
                  setEditing({
                    id: item.id,
                    planId: item.planId,
                    expiresAt: localDateTime(item.planExpiresAt),
                    reason: "",
                  })
                }
              >
                {t("adminEditPlan")}
              </button>
              <button
                type="button"
                disabled={locked || item.id === currentUserId}
                title={
                  item.id === currentUserId ? t("adminSelfFreeze") : undefined
                }
                onClick={() =>
                  void action.perform(() =>
                    client.adminFreezeUser(item.id, !item.frozen),
                  )
                }
              >
                {item.frozen ? (
                  <Undo2 size={16} aria-hidden="true" />
                ) : (
                  <Snowflake size={16} aria-hidden="true" />
                )}
                {t(item.frozen ? "release" : "freeze")}
              </button>
            </div>
            {editing?.id === item.id ? (
              <form
                className="admin-plan-editor form-grid"
                onSubmit={(event) => {
                  event.preventDefault();
                  const draft = editing;
                  void action
                    .perform(() =>
                      client.adminSetUserPlan(item.id, {
                        planId: draft.planId,
                        expiresAt: draft.expiresAt
                          ? new Date(draft.expiresAt).toISOString()
                          : undefined,
                        reason: draft.reason.trim(),
                      }),
                    )
                    .then((saved) => {
                      if (saved) setEditing(null);
                    });
                }}
              >
                <label className="field-row">
                  <span>{t("adminPlan")}</span>
                  <select
                    value={editing.planId}
                    onChange={(event) =>
                      setEditing({ ...editing, planId: event.target.value })
                    }
                  >
                    {plans.map((plan) => (
                      <option key={plan.id} value={plan.id}>
                        {plan.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="field-row">
                  <span>{t("adminExpiresAt")}</span>
                  <input
                    type="datetime-local"
                    value={editing.expiresAt}
                    onChange={(event) =>
                      setEditing({ ...editing, expiresAt: event.target.value })
                    }
                  />
                </label>
                <label className="field-row">
                  <span>{t("adminPlanReason")}</span>
                  <input
                    required
                    maxLength={1000}
                    value={editing.reason}
                    onChange={(event) =>
                      setEditing({ ...editing, reason: event.target.value })
                    }
                  />
                </label>
                <div className="button-row compact">
                  <button
                    type="submit"
                    disabled={
                      locked || !editing.reason.trim() || plans.length === 0
                    }
                  >
                    <Check size={16} aria-hidden="true" />
                    {t("adminSave")}
                  </button>
                  <button
                    type="button"
                    disabled={action.busy}
                    onClick={() => setEditing(null)}
                  >
                    <X size={16} aria-hidden="true" />
                    {t("adminCancel")}
                  </button>
                </div>
              </form>
            ) : null}
          </article>
        ))}
      </div>
      <PageControls
        page={list.page}
        hasNext={list.hasNext}
        busy={locked}
        onPage={(page) => {
          setEditing(null);
          list.go(page);
        }}
        t={t}
      />
    </section>
  );
}

export function AdminFileManager({
  locale,
  t,
  scanDetail,
  statusLabel,
  onChanged,
}: {
  locale: Locale;
  t: Translate;
  scanDetail: (file: AdminAttachment) => {
    label: string;
    description: string;
    tone: string;
  };
  statusLabel: (status: string) => string;
  onChanged: () => void;
}) {
  const list = useAdminPage(loadFiles);
  const action = useAdminAction(onChanged, list.reload, t);
  const locked = list.loading || action.busy;
  return (
    <section
      className="admin-section admin-manager"
      aria-label={t("adminTabFiles")}
    >
      <ListToolbar
        placeholder={t("adminFileSearch")}
        busy={locked}
        onSearch={list.search}
        onRefresh={list.reload}
        t={t}
      />
      {action.feedback}
      <ListState
        loading={list.loading}
        error={list.error}
        empty={list.rows.length === 0}
        t={t}
      />
      <div className="admin-record-list">
        {list.rows.map((file) => {
          const scan = scanDetail(file);
          const removed =
            file.pasteStatus === "taken_down" ||
            ["taken_down", "deleted", "deleting", "purged"].includes(
              file.status,
            );
          return (
            <article className="admin-record" key={file.id}>
              <div className="admin-record-heading">
                <div className="admin-record-title">
                  <FileUp size={18} aria-hidden="true" />
                  <div>
                    <strong>{file.fileName}</strong>
                    <span>{file.pasteTitle || file.pasteId}</span>
                  </div>
                </div>
                <span
                  className={`admin-status admin-status--${scan.tone}`}
                  title={scan.description}
                >
                  {scan.label}
                </span>
              </div>
              <dl className="admin-record-meta">
                <div>
                  <dt>{t("adminOwner")}</dt>
                  <dd>
                    <code title={file.userId}>{file.userId}</code>
                  </dd>
                </div>
                <div>
                  <dt>{t("file")}</dt>
                  <dd>{formatBytes(file.size)}</dd>
                </div>
                <div>
                  <dt>{t("adminStatus")}</dt>
                  <dd>{statusLabel(file.status)}</dd>
                </div>
                {file.pasteStatus ? (
                  <div>
                    <dt>{t("adminContentStatus")}</dt>
                    <dd>
                      {file.pasteStatus === "taken_down"
                        ? t("adminTakenDown")
                        : statusLabel(file.pasteStatus)}
                    </dd>
                  </div>
                ) : null}
                <div>
                  <dt>{t("adminCreatedAt")}</dt>
                  <dd>{dateLabel(file.createdAt, locale, "—")}</dd>
                </div>
              </dl>
              <details className="admin-record-details">
                <summary>{t("adminViewDetails")}</summary>
                <code>{file.id}</code>
                <code>{file.sha256}</code>
              </details>
              <div className="button-row compact admin-record-actions">
                <button
                  type="button"
                  disabled={locked || removed}
                  onClick={() =>
                    void action.perform(
                      () => client.adminRetryScan(file.id),
                      t("scanRetried"),
                    )
                  }
                >
                  <RotateCcw size={16} aria-hidden="true" />
                  {t("retry")}
                </button>
                <button
                  type="button"
                  disabled={locked || removed}
                  onClick={() =>
                    void action.perform(() =>
                      client.adminFreezeAttachment(
                        file.id,
                        file.status !== "frozen",
                      ),
                    )
                  }
                >
                  {file.status === "frozen" ? (
                    <Undo2 size={16} aria-hidden="true" />
                  ) : (
                    <Snowflake size={16} aria-hidden="true" />
                  )}
                  {t(file.status === "frozen" ? "release" : "freeze")}
                </button>
                <button
                  className="admin-danger-button"
                  type="button"
                  disabled={locked || removed}
                  onClick={() => {
                    if (window.confirm(t("adminTakedownConfirm")))
                      void action.perform(
                        () => client.adminTakedownPaste(file.pasteId),
                        t("adminFileTakenDown"),
                      );
                  }}
                >
                  <Ban size={16} aria-hidden="true" />
                  {t("adminTakedown")}
                </button>
              </div>
            </article>
          );
        })}
      </div>
      <PageControls
        page={list.page}
        hasNext={list.hasNext}
        busy={locked}
        onPage={list.go}
        t={t}
      />
    </section>
  );
}

export function AdminShareManager({
  locale,
  t,
  onChanged,
}: {
  locale: Locale;
  t: Translate;
  onChanged: () => void;
}) {
  const list = useAdminPage(loadShares);
  const action = useAdminAction(onChanged, list.reload, t);
  const locked = list.loading || action.busy;
  return (
    <section
      className="admin-section admin-manager"
      aria-label={t("adminTabShares")}
    >
      <ListToolbar busy={locked} onRefresh={list.reload} t={t} />
      {action.feedback}
      <ListState
        loading={list.loading}
        error={list.error}
        empty={list.rows.length === 0}
        t={t}
      />
      <div className="admin-record-list">
        {list.rows.map((share) => (
          <article className="admin-record" key={share.id}>
            <div className="admin-record-heading">
              <div className="admin-record-title">
                <Link2 size={18} aria-hidden="true" />
                <strong>{share.id}</strong>
              </div>
              <span
                className={`admin-status admin-status--${share.revokedAt || Date.parse(share.expiresAt) <= Date.now() ? "neutral" : "success"}`}
              >
                {t(
                  share.revokedAt
                    ? "adminShareRevoked"
                    : Date.parse(share.expiresAt) <= Date.now()
                      ? "adminShareExpired"
                      : "active",
                )}
              </span>
            </div>
            <dl className="admin-record-meta">
              <div>
                <dt>{t("adminOwner")}</dt>
                <dd>
                  <code title={share.userId}>{share.userId}</code>
                </dd>
              </div>
              <div>
                <dt>{t("visits")}</dt>
                <dd>{share.visitCount}</dd>
              </div>
              <div>
                <dt>{t("downloads")}</dt>
                <dd>{share.downloadCount}</dd>
              </div>
              <div>
                <dt>{t("adminExpiresAt")}</dt>
                <dd>
                  {dateLabel(share.expiresAt, locale, t("adminNeverExpires"))}
                </dd>
              </div>
            </dl>
            <div className="button-row compact admin-record-actions">
              <button
                className="admin-danger-button"
                type="button"
                disabled={locked || Boolean(share.revokedAt)}
                onClick={() =>
                  void action.perform(
                    () => client.adminRevokeShare(share.id),
                    t("shareRevoked"),
                  )
                }
              >
                <Ban size={16} aria-hidden="true" />
                {t("revoke")}
              </button>
            </div>
          </article>
        ))}
      </div>
      <PageControls
        page={list.page}
        hasNext={list.hasNext}
        busy={locked}
        onPage={list.go}
        t={t}
      />
    </section>
  );
}

const auditLabels: Record<string, string> = {
  "auth.google_oauth": "auditLogin",
  "auth.github_oauth": "auditLogin",
  "admin.catalog_entries_update": "auditCatalog",
  "admin.catalog_update": "auditCatalog",
  "admin.runtime_config_update": "auditRuntime",
  "admin.managed_config_update": "auditManaged",
  "admin.user_plan_set": "auditUserPlan",
  "admin.user_freeze": "auditUserFreeze",
  "admin.attachment_freeze": "auditFileFreeze",
  "admin.scan_retry": "auditScan",
  "admin.paste_takedown": "auditTakedown",
  "admin.share_revoke": "auditShare",
  "auth.oauth_linked": "auditOAuthLinked",
  "auth.oauth_unlinked": "auditOAuthUnlinked",
  "admin.report_status": "auditReport",
  "admin.webhook_replay": "auditWebhook",
  "admin.redemption_batch_create": "auditRedemptionCreate",
  "admin.redemption_batch_update": "auditRedemptionUpdate",
  "admin.provider_test": "auditProvider",
  "admin.alert_test": "auditAlert",
};

export function AdminAuditLog({
  logs,
  locale,
  t,
  fallbackLabel,
}: {
  logs: AuditLog[];
  locale: Locale;
  t: Translate;
  fallbackLabel: (value: string) => string;
}) {
  const [page, setPage] = useState(0);
  const lastPage = Math.max(0, Math.ceil(logs.length / pageSize) - 1);
  const currentPage = Math.min(page, lastPage);
  return (
    <section className="admin-audit-section" aria-label={t("adminTabAudit")}>
      {logs.length === 0 ? (
        <div className="admin-empty">{t("adminNoAudit")}</div>
      ) : (
        <div className="admin-table-scroll">
          <table className="admin-audit-table">
            <thead>
              <tr>
                <th scope="col">{t("adminAuditTime")}</th>
                <th scope="col">{t("adminAuditAction")}</th>
                <th scope="col">{t("adminAuditActor")}</th>
                <th scope="col">{t("adminAuditTarget")}</th>
              </tr>
            </thead>
            <tbody>
              {logs
                .slice(currentPage * pageSize, (currentPage + 1) * pageSize)
                .map((log) => (
                  <tr key={log.id}>
                    <td>
                      <time dateTime={log.createdAt}>
                        {dateLabel(log.createdAt, locale, "—")}
                      </time>
                    </td>
                    <td>
                      <strong title={log.action}>
                        {auditLabels[log.action]
                          ? t(auditLabels[log.action])
                          : fallbackLabel(log.action)}
                      </strong>
                    </td>
                    <td>
                      <Identifier
                        value={log.actorId || t("adminAuditSystem")}
                        t={t}
                      />
                    </td>
                    <td>
                      <Identifier value={log.target} t={t} />
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      )}
      <PageControls
        page={currentPage}
        hasNext={currentPage < lastPage}
        busy={false}
        onPage={setPage}
        t={t}
      />
    </section>
  );
}

function Identifier({ value, t }: { value: string; t: Translate }): ReactNode {
  return (
    <details className="admin-identifier">
      <summary title={value} aria-label={`${t("adminViewDetails")}: ${value}`}>
        {value || "—"}
      </summary>
      <code>{value}</code>
    </details>
  );
}
