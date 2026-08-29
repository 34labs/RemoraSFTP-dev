import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api, ApiError, type Entry, type SessionInfo } from '../api/client';
import { useI18n } from '../i18n/i18n';
import { useStore } from '../state/store';
import { Icon, fileIconFor } from '../components/Icons';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { Inspector } from '../components/Inspector';
import { Preview } from '../components/Preview';
import { formatBytes, formatDate, parentPath, joinPath, baseName } from '../util/format';
import type { View } from '../App';

type SortKey = 'name' | 'size' | 'modified' | 'type';

export function FilesView({ onNavigate }: { onNavigate?: (v: View) => void }) {
  const { t } = useI18n();
  const store = useStore();
  const { sessions, connections, settings, notify } = store;

  const [session, setSession] = useState<SessionInfo | null>(null);
  const [entries, setEntries] = useState<Entry[]>([]);
  const [path, setPath] = useState('/');
  const [loading, setLoading] = useState(false);
  const [view, setView] = useState<'list' | 'grid'>(settings?.defaultView ?? 'list');
  const [search, setSearch] = useState('');
  const [sortKey, setSortKey] = useState<SortKey>('name');
  const [sortAsc, setSortAsc] = useState(true);
  const [showHidden] = useState(settings?.showHidden ?? false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [menu, setMenu] = useState<{ x: number; y: number; entry?: Entry } | null>(null);
  const [inspect, setInspect] = useState<Entry | null>(null);
  const [preview, setPreview] = useState<Entry | null>(null);
  const [renaming, setRenaming] = useState<Entry | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [dragOver, setDragOver] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const lastClickRef = useRef<{ id: string; index: number; time: number } | null>(null);

  // Pick an active connected session.
  useEffect(() => {
    const connected = sessions.find((s) => s.state === 'connected');
    if (connected && (!session || session.id !== connected.id)) {
      setSession(connected);
      setPath(connected.cwd || connected.startDir || '/');
    }
    if (!connected && session) {
      setSession(null);
    }
  }, [sessions, session]);

  const list = useCallback(
    async (p: string) => {
      if (!session) return;
      setLoading(true);
      try {
        const r = await api.list(session.id, p);
        setEntries(r.entries ?? []);
        setPath(r.path);
        setSelected(new Set());
      } catch (e) {
        notify('error', e instanceof Error ? e.message : 'List failed');
      } finally {
        setLoading(false);
      }
    },
    [session, notify],
  );

  useEffect(() => {
    if (session) void list(path);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session?.id]);

  const refresh = useCallback(() => void list(path), [list, path]);

  const crumbs = useMemo(() => {
    const parts = path.split('/').filter(Boolean);
    const out: { name: string; path: string }[] = [{ name: t('files.breadcrumbHome'), path: '/' }];
    let acc = '';
    for (const p of parts) {
      acc += '/' + p;
      out.push({ name: p, path: acc });
    }
    return out;
  }, [path, t]);

  const filtered = useMemo(() => {
    let list = entries.filter((e) => {
      if (!showHidden && e.name.startsWith('.')) return false;
      if (search && !e.name.toLowerCase().includes(search.toLowerCase())) return false;
      return true;
    });
    list = [...list].sort((a, b) => {
      // Folders always first.
      if ((a.type === 'dir') !== (b.type === 'dir')) return a.type === 'dir' ? -1 : 1;
      let cmp = 0;
      switch (sortKey) {
        case 'size':
          cmp = a.size - b.size;
          break;
        case 'modified':
          cmp = new Date(a.modTime ?? 0).getTime() - new Date(b.modTime ?? 0).getTime();
          break;
        case 'type':
          cmp = (a.mimeType ?? '').localeCompare(b.mimeType ?? '');
          break;
        default:
          cmp = a.name.localeCompare(b.name, undefined, { numeric: true });
      }
      return sortAsc ? cmp : -cmp;
    });
    return list;
  }, [entries, search, sortKey, sortAsc, showHidden]);

  const selectedEntries = useMemo(
    () => filtered.filter((e) => selected.has(e.path)),
    [filtered, selected],
  );

  const connect = async (connId: string) => {
    try {
      await api.connect(connId);
      await store.refreshSessions();
      notify('success', t('connections.connected'));
    } catch (e) {
      if (!(e instanceof ApiError && (e.code === 'untrusted-host-key' || e.code === 'untrusted-certificate'))) {
        notify('error', e instanceof Error ? e.message : 'Connect failed');
      }
      // Trust prompts are driven via WebSocket / the error is stored.
    }
  };

  const openEntry = (e: Entry) => {
    if (e.type === 'dir') {
      void list(e.path);
    } else if (e.isSymlink && e.linkTarget) {
      // Treat symlink click conservatively: show properties.
      setInspect(e);
    } else {
      setPreview(e);
    }
  };

  const onRowClick = (e: Entry, index: number, ev: React.MouseEvent) => {
    if (ev.metaKey || ev.ctrlKey) {
      setSelected((s) => {
        const n = new Set(s);
        n.has(e.path) ? n.delete(e.path) : n.add(e.path);
        return n;
      });
      return;
    }
    if (ev.shiftKey && lastClickRef.current) {
      const last = lastClickRef.current.index;
      const [lo, hi] = [Math.min(last, index), Math.max(last, index)];
      const next = new Set(selected);
      filtered.slice(lo, hi + 1).forEach((x) => next.add(x.path));
      setSelected(next);
      return;
    }
    const now = Date.now();
    const last = lastClickRef.current;
    setSelected(new Set([e.path]));
    lastClickRef.current = { id: e.path, index, time: now };
    if (last && last.id === e.path && now - last.time < 450) {
      openEntry(e);
    }
  };

  const uploadFiles = useCallback(
    async (files: FileList | File[]) => {
      if (!session) return;
      const arr = Array.from(files);
      for (const f of arr) {
        try {
          // Stream the body directly; server pipes to remote without buffering.
          await fetch(api.uploadUrl(session.id, path, f.name), {
            method: 'POST',
            headers: {
              Authorization: `Bearer ${sessionStorage.getItem('remorasftp.token')}`,
              'X-Requested-With': 'RemoraSFTP',
            },
            body: f,
          });
          notify('success', t('live.transferStarted', { name: f.name }));
        } catch (e) {
          notify('error', e instanceof Error ? e.message : 'Upload failed');
        }
      }
      void list(path);
      if (onNavigate) onNavigate('transfers');
    },
    [session, path, notify, list, onNavigate, t],
  );

  const download = async (e: Entry) => {
    if (!session) return;
    try {
      const res = await fetch(api.downloadUrl(session.id, e.path), {
        headers: { Authorization: `Bearer ${sessionStorage.getItem('remorasftp.token')}` },
      });
      if (!res.ok) throw new Error('Download failed');
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = e.name;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      notify('success', t('files.downloadDone'));
    } catch (err) {
      notify('error', err instanceof Error ? err.message : 'Download failed');
    }
  };

  const remove = async (items: Entry[]) => {
    if (!session) return;
    const dirs = items.filter((i) => i.type === 'dir');
    const msg =
      items.length > 1
        ? t('files.deleteConfirmMany', { count: items.length })
        : t('files.deleteConfirm', { name: items[0].name });
    if (settings?.confirmDeletes && !window.confirm(msg)) return;
    for (const item of items) {
      try {
        await api.remove(session.id, item.path, dirs.some((d) => d.path === item.path));
      } catch (e) {
        notify('error', e instanceof Error ? e.message : 'Delete failed');
      }
    }
    notify('info', 'Deleted');
    void refresh();
  };

  const doRename = async () => {
    if (!session || !renaming) {
      setRenaming(null);
      return;
    }
    const target = joinPath(parentPath(renaming.path), renameValue);
    try {
      await api.rename(session.id, renaming.path, target);
      notify('success', 'Renamed');
      void refresh();
    } catch (e) {
      notify('error', e instanceof Error ? e.message : 'Rename failed');
    }
    setRenaming(null);
  };

  const newFolder = async () => {
    if (!session) return;
    const name = window.prompt(t('files.folderName'), t('files.newFolderName'));
    if (!name) return;
    try {
      await api.mkdir(session.id, joinPath(path, name));
      void refresh();
    } catch (e) {
      notify('error', e instanceof Error ? e.message : 'Mkdir failed');
    }
  };

  const menuItems = useCallback(
    (entry?: Entry): MenuItem[] => {
      const caps = session?.caps ?? session?.capabilities;
      const items: MenuItem[] = [];
      if (entry) {
        items.push(
          { label: t('files.preview'), icon: <Icon.Eye />, onClick: () => setPreview(entry), disabled: entry.type === 'dir' },
          { label: t('files.download'), icon: <Icon.Download />, onClick: () => void download(entry), disabled: entry.type === 'dir' },
          { sep: true },
          { label: t('files.rename'), icon: <Icon.Edit />, onClick: () => { setRenaming(entry); setRenameValue(entry.name); } },
          { label: t('files.properties'), icon: <Icon.Settings />, onClick: () => setInspect(entry) },
          { sep: true },
          { label: t('files.delete'), icon: <Icon.Trash />, danger: true, onClick: () => void remove([entry]) },
        );
        void caps;
      } else {
        items.push(
          { label: t('files.newFolder'), icon: <Icon.Folder />, onClick: () => void newFolder() },
          { label: t('files.upload'), icon: <Icon.Upload />, onClick: () => fileInputRef.current?.click() },
          { label: t('files.refresh'), icon: <Icon.Refresh />, onClick: refresh },
        );
      }
      return items;
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [session, t, refresh, path],
  );

  // Keyboard navigation for the file area.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
      if (e.key === 'Delete' && selected.size) {
        void remove(selectedEntries);
      }
      if (e.key === 'Backspace') {
        if (path !== '/') void list(parentPath(path));
      }
      if (e.key === 'F5') {
        e.preventDefault();
        refresh();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected, path, selectedEntries]);

  if (!session) {
    return (
      <div className="empty-state">
        <Icon.Plug className="big-icon" />
        <h2 style={{ margin: '0 0 6px' }}>{t('connections.selectToConnect')}</h2>
        <div style={{ display: 'grid', gap: 8, marginTop: 14 }}>
          {connections.map((c) => (
            <button key={c.id} className="btn" onClick={() => void connect(c.id)}>
              <Icon.Plug size={15} />
              {c.name}
              <span className="muted mono">
                {c.protocol}://{c.host}:{c.port}
              </span>
            </button>
          ))}
          {connections.length === 0 && <p className="muted">{t('connections.noneHint')}</p>}
        </div>
      </div>
    );
  }

  const caps = session.caps ?? session.capabilities;

  return (
    <div
      style={{ flex: 1, display: 'flex', minHeight: 0, position: 'relative' }}
      onDragOver={(e) => {
        e.preventDefault();
        setDragOver(true);
      }}
      onDragLeave={() => setDragOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragOver(false);
        if (e.dataTransfer.files?.length) void uploadFiles(e.dataTransfer.files);
      }}
    >
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        {/* Toolbar */}
        <div className="toolbar">
          <button className="btn icon" title={t('files.refresh')} onClick={refresh} aria-label={t('files.refresh')}>
            <Icon.Refresh />
          </button>
          <div className="breadcrumbs" aria-label="Breadcrumb">
            {crumbs.map((c, i) => (
              <span key={c.path} className="row" style={{ gap: 0 }}>
                <button
                  className={`crumb ${i === crumbs.length - 1 ? 'current' : ''}`}
                  onClick={() => void list(c.path)}
                  aria-current={i === crumbs.length - 1 ? 'page' : undefined}
                >
                  {c.name}
                </button>
                {i < crumbs.length - 1 && <span className="crumb-sep">/</span>}
              </span>
            ))}
          </div>
          <div className="grow" />
          <div className="row" style={{ position: 'relative' }}>
            <Icon.Search size={15} style={{ position: 'absolute', left: 10, color: 'var(--text-faint)' }} />
            <input
              className="input"
              style={{ width: 180, paddingLeft: 32 }}
              placeholder={t('files.search')}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              aria-label={t('files.search')}
            />
          </div>
          <button className="btn icon" title={t('files.newFolder')} onClick={() => void newFolder()} aria-label={t('files.newFolder')}>
            <Icon.Folder />
          </button>
          <button className="btn primary" onClick={() => fileInputRef.current?.click()}>
            <Icon.Upload size={15} />
            {t('files.upload')}
          </button>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            style={{ display: 'none' }}
            onChange={(e) => e.target.files && void uploadFiles(e.target.files)}
          />
          <div className="row" style={{ gap: 2, marginLeft: 4 }}>
            <button
              className={`btn icon ${view === 'list' ? '' : 'ghost'}`}
              onClick={() => setView('list')}
              aria-label={t('files.listView')}
              aria-pressed={view === 'list'}
            >
              <Icon.List />
            </button>
            <button
              className={`btn icon ${view === 'grid' ? '' : 'ghost'}`}
              onClick={() => setView('grid')}
              aria-label={t('files.gridView')}
              aria-pressed={view === 'grid'}
            >
              <Icon.Grid />
            </button>
          </div>
        </div>

        {caps && !caps.encrypted && (
          <div
            style={{
              background: 'var(--warning-subtle)',
              color: 'var(--warning)',
              padding: '7px 16px',
              fontSize: 12.5,
              fontWeight: 500,
            }}
            role="alert"
          >
            ⚠ {t('files.capabilities.plainFtp')}
          </div>
        )}

        {/* File area */}
        <div className="file-area" onClick={() => setSelected(new Set())} onContextMenu={(e) => { e.preventDefault(); setMenu({ x: e.clientX, y: e.clientY }); }}>
          {loading && <p className="muted" style={{ padding: 16 }}>{t('common.loading')}</p>}
          {!loading && filtered.length === 0 && (
            <div className="empty-state">
              <Icon.Folder className="big-icon" />
              <p>{entries.length === 0 ? t('files.empty') : t('common.noResults')}</p>
              <p className="faint">{t('files.emptyHint')}</p>
            </div>
          )}

          {view === 'list' && !loading && filtered.length > 0 && (
            <table className="files" onClick={(e) => e.stopPropagation()}>
              <thead>
                <tr>
                  <th onClick={() => { setSortKey('name'); setSortAsc(!sortAsc); }} style={{ cursor: 'pointer' }}>{t('files.name')}</th>
                  <th onClick={() => { setSortKey('size'); setSortAsc(!sortAsc); }} style={{ cursor: 'pointer', width: 100 }}>{t('files.size')}</th>
                  <th onClick={() => { setSortKey('modified'); setSortAsc(!sortAsc); }} style={{ cursor: 'pointer', width: 170 }}>{t('files.modified')}</th>
                  <th style={{ width: 110 }}>{t('files.permissions')}</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((e, i) => {
                  const IconCmp = fileIconFor(e.name, e.type, e.isSymlink);
                  const isSel = selected.has(e.path);
                  return (
                    <tr
                      key={e.path}
                      className={`file-row ${isSel ? 'selected' : ''}`}
                      onClick={(ev) => onRowClick(e, i, ev)}
                      onDoubleClick={() => openEntry(e)}
                      onContextMenu={(ev) => {
                        ev.preventDefault();
                        ev.stopPropagation();
                        if (!isSel) setSelected(new Set([e.path]));
                        setMenu({ x: ev.clientX, y: ev.clientY, entry: e });
                      }}
                    >
                      <td>
                        <span className="file-name-cell">
                          <IconCmp className={`file-icon ${e.type === 'dir' ? 'folder' : ''}`} />
                          {renaming?.path === e.path ? (
                            <input
                              className="input"
                              style={{ maxWidth: 280 }}
                              autoFocus
                              value={renameValue}
                              onChange={(ev) => setRenameValue(ev.target.value)}
                              onBlur={() => void doRename()}
                              onKeyDown={(ev) => {
                                if (ev.key === 'Enter') void doRename();
                                if (ev.key === 'Escape') setRenaming(null);
                              }}
                            />
                          ) : (
                            <span className="file-name" title={e.linkTarget ? `${e.name} → ${e.linkTarget}` : e.name}>
                              {e.name}
                              {e.isSymlink && <span className="link-badge"> ↗</span>}
                            </span>
                          )}
                        </span>
                      </td>
                      <td className="muted mono" style={{ fontSize: 12.5 }}>
                        {e.type === 'dir' ? '-' : formatBytes(e.size)}
                      </td>
                      <td className="muted" style={{ fontSize: 12.5 }}>
                        {formatDate(e.modTime)}
                      </td>
                      <td className="mono muted" style={{ fontSize: 12 }}>
                        {e.permissions || '-'}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}

          {view === 'grid' && !loading && (
            <div className="grid-view" onClick={(e) => e.stopPropagation()}>
              {filtered.map((e, i) => {
                const IconCmp = fileIconFor(e.name, e.type, e.isSymlink);
                const isSel = selected.has(e.path);
                return (
                  <div
                    key={e.path}
                    className={`grid-item ${isSel ? 'selected' : ''}`}
                    onClick={(ev) => onRowClick(e, i, ev)}
                    onDoubleClick={() => openEntry(e)}
                    onContextMenu={(ev) => {
                      ev.preventDefault();
                      ev.stopPropagation();
                      setMenu({ x: ev.clientX, y: ev.clientY, entry: e });
                    }}
                    title={e.name}
                  >
                    <IconCmp className={`file-icon ${e.type === 'dir' ? 'folder' : ''}`} />
                    <span className="file-name">{e.name}</span>
                    <span className="faint" style={{ fontSize: 11 }}>
                      {e.type === 'dir' ? '' : formatBytes(e.size)}
                    </span>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {dragOver && <div className="drop-overlay">{t('files.dropToUpload', { path })}</div>}
      </div>

      {inspect && <Inspector entry={inspect} session={session} onClose={() => setInspect(null)} onChange={refresh} />}
      {preview && <Preview entry={preview} session={session} onClose={() => setPreview(null)} onDownload={download} />}
      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          items={menuItems(menu.entry)}
          onClose={() => setMenu(null)}
        />
      )}
    </div>
  );
}
