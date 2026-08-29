import { useEffect, useMemo, useState } from 'react';
import { api, type Entry, type SessionInfo } from '../api/client';
import { useI18n } from '../i18n/i18n';
import { Modal } from './Modal';
import { Icon } from './Icons';
import { base64ToText, highlight } from '../util/highlight';
import { formatBytes, formatDate } from '../util/format';

type PreviewState =
  | { kind: 'loading' }
  | { kind: 'text'; content: string; truncated: boolean; mime: string; sourceNote?: string }
  | { kind: 'unsupported'; message?: string };

export function Preview({
  entry,
  session,
  onClose,
  onDownload,
}: {
  entry: Entry;
  session: SessionInfo;
  onClose: () => void;
  onDownload: (e: Entry) => void;
}) {
  const { t } = useI18n();
  const [state, setState] = useState<PreviewState>({ kind: 'loading' });

  const mediaUrl = api.previewUrl(session.id, entry.path);
  const mime = entry.mimeType ?? '';

  const isImage = mime.startsWith('image/');
  const isVideo = mime.startsWith('video/');
  const isAudio = mime.startsWith('audio/');
  const isPdf = mime === 'application/pdf';
  const isHtml = mime === 'text/html' || /\.x?html?$/i.test(entry.name);
  const isMedia = isImage || isVideo || isAudio;

  useEffect(() => {
    if (isMedia || isPdf || isHtml) {
      setState({ kind: 'loading' });
      return; // rendered directly from URL
    }
    let cancelled = false;
    api
      .previewText(session.id, entry.path)
      .then((r) => {
        if (cancelled) return;
        if (r.kind === 'unsupported') {
          setState({ kind: 'unsupported', message: r.message });
          return;
        }
        const content = base64ToText(r.content ?? '');
        setState({
          kind: 'text',
          content,
          truncated: r.truncated ?? false,
          mime: r.mime,
          sourceNote: r.sourceNote,
        });
      })
      .catch(() => setState({ kind: 'unsupported' }));
    return () => {
      cancelled = true;
    };
  }, [session.id, entry.path, isMedia, isPdf, isHtml]);

  const authedUrl = useAuthedMediaUrl(mediaUrl, isMedia || isPdf || isHtml);

  const lang = languageFor(entry.name, mime);
  const code = state.kind === 'text' ? state.content : '';

  return (
    <Modal
      title={`${t('preview.title')} - ${entry.name}`}
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={() => onDownload(entry)}>
            <Icon.Download size={15} />
            {t('files.download')}
          </button>
          <button className="btn primary" onClick={onClose}>
            {t('common.close')}
          </button>
        </>
      }
    >
      {isImage && (
        <div className="preview-wrap">
          <img className="media-preview" src={authedUrl} alt={entry.name} />
        </div>
      )}
      {isVideo && (
        <div className="preview-wrap">
          <video className="media-preview" src={authedUrl} controls autoPlay />
        </div>
      )}
      {isAudio && (
        <div className="preview-wrap">
          <audio src={authedUrl} controls autoPlay style={{ width: '100%' }} />
        </div>
      )}
      {isPdf && (
        <div style={{ height: '70vh' }}>
          <iframe className="preview-frame" src={authedUrl} title={entry.name} />
        </div>
      )}
      {isHtml && (
        <>
          <p className="hint" style={{ margin: '0 0 8px' }}>
            🔒 {t('preview.htmlIsolated')}
          </p>
          <div style={{ height: '65vh' }}>
            {/* sandbox WITHOUT allow-same-origin and WITHOUT allow-scripts:
                the remote HTML cannot read our origin, the token, or script
                itself. Server also sends a locking CSP. */}
            <iframe
              className="preview-frame"
              src={authedUrl}
              sandbox=""
              title={entry.name}
              referrerPolicy="no-referrer"
            />
          </div>
        </>
      )}
      {!isMedia && !isPdf && !isHtml && state.kind === 'loading' && (
        <p className="muted">{t('common.loading')}</p>
      )}
      {!isMedia && !isPdf && !isHtml && state.kind === 'unsupported' && (
        <div className="empty-state" style={{ padding: 30 }}>
          <Icon.File className="big-icon" />
          <p>{state.message || t('preview.downloadToView')}</p>
        </div>
      )}
      {!isMedia && !isPdf && !isHtml && state.kind === 'text' && (
        <>
          {state.sourceNote && (
            <p
              className="hint"
              style={{
                background: 'var(--accent-subtle)',
                color: 'var(--accent)',
                padding: '8px 12px',
                borderRadius: 7,
                margin: '0 0 10px',
              }}
            >
              ⓘ {state.sourceNote}
            </p>
          )}
          {isSourceCode(entry.name, mime) || lang ? (
            <pre className="code-view" aria-label="Source code">
              <code dangerouslySetInnerHTML={{ __html: highlight(code, lang) }} />
            </pre>
          ) : (
            <pre className="code-view" aria-label="Text preview">
              {code}
            </pre>
          )}
          {state.truncated && <p className="hint">… {t('preview.truncated')}</p>}
        </>
      )}

      <div style={{ marginTop: 12 }}>
        <dl className="kv">
          <dt>{t('inspector.size')}</dt>
          <dd>{formatBytes(entry.size)}</dd>
          <dt>{t('inspector.modified')}</dt>
          <dd>{formatDate(entry.modTime)}</dd>
          <dt>{t('inspector.path')}</dt>
          <dd className="mono" style={{ wordBreak: 'break-all' }}>{entry.path}</dd>
        </dl>
      </div>
    </Modal>
  );
}

function languageFor(name: string, mime: string): string {
  const ext = name.split('.').pop()?.toLowerCase() ?? '';
  const map: Record<string, string> = {
    go: 'go', rs: 'rust', py: 'python', rb: 'ruby', php: 'php', js: 'javascript', mjs: 'javascript',
    cjs: 'javascript', ts: 'typescript', tsx: 'tsx', jsx: 'jsx', java: 'java', c: 'c', h: 'c',
    cpp: 'cpp', cc: 'cpp', hpp: 'cpp', cs: 'csharp', sh: 'bash', bash: 'bash', zsh: 'bash',
    json: 'json', xml: 'xml', yaml: 'yaml', yml: 'yaml', toml: 'toml', md: 'markdown',
    markdown: 'markdown', csv: 'csv', sql: 'sql', html: 'html', css: 'css', vue: 'vue',
    dockerfile: 'dockerfile', proto: 'protobuf',
  };
  if (ext === 'json' || mime.includes('json')) return 'json';
  if (['xml'].includes(ext) || mime.includes('xml')) return 'xml';
  if (['yaml', 'yml'].includes(ext) || mime.includes('yaml')) return 'yaml';
  if (ext === 'toml' || mime.includes('toml')) return 'toml';
  if (ext === 'md' || mime.includes('markdown')) return 'markdown';
  if (ext === 'csv' || mime.includes('csv')) return 'csv';
  return map[ext] ?? '';
}

function isSourceCode(name: string, mime: string): boolean {
  return /\.(go|rs|py|rb|php|js|mjs|cjs|ts|tsx|jsx|java|c|h|cc|cpp|hpp|cs|sh|bash|zsh|swift|kt|scala|sql|proto|vue|svelte|lua|pl|dart|ex|erl|hs|groovy|gradle|tf)$/i.test(
    name,
  ) || mime.startsWith('text/x-') || mime === 'application/x-sh';
}

// Fetch media via authenticated request and expose as an object URL so the
// Authorization header (not a URL token) is used.
function useAuthedMediaUrl(url: string, enabled: boolean): string {
  const [blobUrl, setBlobUrl] = useState('');
  useEffect(() => {
    if (!enabled) return;
    const token = sessionStorage.getItem('remorasftp.token');
    let revoked = false;
    fetch(url, { headers: { Authorization: `Bearer ${token}` } })
      .then((r) => r.blob())
      .then((b) => {
        const u = URL.createObjectURL(b);
        if (!revoked) setBlobUrl(u);
      })
      .catch(() => { });
    return () => {
      revoked = true;
      if (blobUrl) URL.revokeObjectURL(blobUrl);
    };
  }, [url, enabled]);
  return blobUrl;
}
