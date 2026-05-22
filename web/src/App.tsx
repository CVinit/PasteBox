import { useEffect, useMemo, useState } from 'react';
import {
  Clock3,
  Copy,
  FileUp,
  FolderClock,
  Image,
  Link2,
  LockKeyhole,
  Search,
  ShieldCheck,
  Sparkles,
  UploadCloud,
} from 'lucide-react';

import { fetchPlanCatalog, type Plan, type PlanCatalog } from './api';
import './styles.css';

type PasteDraft = {
  title: string;
  body: string;
  expiresIn: string;
  tags: string;
};

const initialDraft: PasteDraft = {
  title: '',
  body: '',
  expiresIn: '24h',
  tags: '',
};

const samplePastes = [
  {
    title: 'Release checklist',
    summary: 'Verify webhook idempotency, quota guards, cleanup retry queues, and audit logs.',
    meta: 'Text · 2 tags · expires in 18h',
    shared: false,
  },
  {
    title: 'Design references',
    summary: '4 images and one ZIP ready for a private review link.',
    meta: 'Files · scan clean · expires in 3d',
    shared: true,
  },
  {
    title: 'Migration notes',
    summary: 'PostgreSQL tables for pastes, attachments, shares, quotas, billing, and audit logs.',
    meta: 'Text · pinned · expires in 12d',
    shared: false,
  },
];

function App() {
  const [draft, setDraft] = useState<PasteDraft>(initialDraft);
  const [catalog, setCatalog] = useState<PlanCatalog | null>(null);

  useEffect(() => {
    void fetchPlanCatalog().then(setCatalog);
  }, []);

  const activePlan = useMemo(() => catalog?.plans.find((plan) => plan.id === 'free'), [catalog]);

  return (
    <main className="app-shell">
      <aside className="sidebar" aria-label="PasteBox navigation">
        <div className="brand-mark" aria-label="PasteBox">
          <div className="brand-icon">
            <Copy size={22} aria-hidden="true" />
          </div>
          <div>
            <strong>PasteBox</strong>
            <span>Private cloud clipboard</span>
          </div>
        </div>

        <nav className="nav-list">
          <a className="nav-item active" href="#inbox">
            <FolderClock size={18} aria-hidden="true" />
            Inbox
          </a>
          <a className="nav-item" href="#shared">
            <Link2 size={18} aria-hidden="true" />
            Shared
          </a>
          <a className="nav-item" href="#files">
            <FileUp size={18} aria-hidden="true" />
            Files
          </a>
          <a className="nav-item" href="#security">
            <ShieldCheck size={18} aria-hidden="true" />
            Security
          </a>
        </nav>

        <section className="quota-panel" aria-label="Current quota">
          <div>
            <span className="eyebrow">Current plan</span>
            <strong>{activePlan?.name ?? 'Free'}</strong>
          </div>
          <div className="quota-bar">
            <span />
          </div>
          <p>
            {formatBytes(activePlan?.activeStorageBytes ?? 500 * 1024 * 1024)} active storage ·{' '}
            {activePlan?.activePasteLimit ?? 20} active pastes
          </p>
        </section>
      </aside>

      <section className="workspace">
        <header className="topbar">
          <label className="search-box">
            <Search size={18} aria-hidden="true" />
            <input type="search" placeholder="Search private pastes, filenames, and tags" />
          </label>
          <button className="icon-button" type="button" aria-label="Upload files" title="Upload files">
            <UploadCloud size={19} aria-hidden="true" />
          </button>
        </header>

        <section className="composer" aria-labelledby="new-paste-title">
          <div className="composer-heading">
            <div>
              <span className="eyebrow">New private paste</span>
              <h1 id="new-paste-title">PasteBox</h1>
            </div>
            <div className="privacy-badge">
              <LockKeyhole size={16} aria-hidden="true" />
              Private by default
            </div>
          </div>

          <input
            className="title-input"
            value={draft.title}
            onChange={(event) => setDraft((current) => ({ ...current, title: event.target.value }))}
            placeholder="Optional title"
          />
          <textarea
            value={draft.body}
            onChange={(event) => setDraft((current) => ({ ...current, body: event.target.value }))}
            placeholder="Paste text here, or press Ctrl/Cmd+V to add text, images, and files."
          />

          <div className="composer-controls">
            <label>
              <Clock3 size={16} aria-hidden="true" />
              <select
                value={draft.expiresIn}
                onChange={(event) => setDraft((current) => ({ ...current, expiresIn: event.target.value }))}
              >
                <option value="24h">24 hours</option>
                <option value="7d">7 days</option>
                <option value="30d">30 days</option>
              </select>
            </label>
            <input
              value={draft.tags}
              onChange={(event) => setDraft((current) => ({ ...current, tags: event.target.value }))}
              placeholder="tags, comma separated"
            />
            <button type="button">
              <Sparkles size={16} aria-hidden="true" />
              Create paste
            </button>
          </div>

          <div className="drop-zone">
            <Image size={20} aria-hidden="true" />
            Drop images or files here
          </div>
        </section>

        <section className="content-grid">
          <div className="paste-list" id="inbox">
            <div className="section-heading">
              <h2>Recent pastes</h2>
              <span>Newest first</span>
            </div>
            {samplePastes.map((paste) => (
              <article className="paste-card" key={paste.title}>
                <div>
                  <h3>{paste.title}</h3>
                  <p>{paste.summary}</p>
                  <span>{paste.meta}</span>
                </div>
                <button className="icon-button small" type="button" aria-label={`Copy ${paste.title}`}>
                  <Copy size={17} aria-hidden="true" />
                </button>
                {paste.shared ? <span className="share-chip">Shared</span> : null}
              </article>
            ))}
          </div>

          <aside className="plan-list" aria-label="Plan limits">
            <div className="section-heading">
              <h2>Plan limits</h2>
              <span>UTC daily windows</span>
            </div>
            {(catalog?.plans ?? []).map((plan) => (
              <PlanCard plan={plan} key={plan.id} />
            ))}
          </aside>
        </section>
      </section>
    </main>
  );
}

function PlanCard({ plan }: { plan: Plan }) {
  return (
    <article className="plan-card">
      <div>
        <strong>{plan.name}</strong>
        <span>{plan.activePasteLimit.toLocaleString()} active pastes</span>
      </div>
      <dl>
        <div>
          <dt>Storage</dt>
          <dd>{formatBytes(plan.activeStorageBytes)}</dd>
        </div>
        <div>
          <dt>Text</dt>
          <dd>{formatBytes(plan.singleTextBytes)}</dd>
        </div>
        <div>
          <dt>File</dt>
          <dd>{formatBytes(plan.singleFileBytes)}</dd>
        </div>
        <div>
          <dt>Retention</dt>
          <dd>{formatDuration(plan.maxRetentionSeconds)}</dd>
        </div>
      </dl>
    </article>
  );
}

function formatBytes(bytes: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unitIndex = 0;

  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }

  return `${Number.isInteger(value) ? value : value.toFixed(1)} ${units[unitIndex]}`;
}

function formatDuration(seconds: number): string {
  const days = seconds / 86400;
  if (days >= 1) {
    return `${days.toLocaleString()}d`;
  }
  return `${Math.round(seconds / 3600)}h`;
}

export default App;

