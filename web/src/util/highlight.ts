// Small dependency-free syntax highlighter. It ALWAYS escapes HTML first, so
// even malformed or adversarial remote source cannot inject markup. Colors
// are applied via <span> tokens after escaping.

export function base64ToText(b64: string): string {
  try {
    const bin = atob(b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return new TextDecoder().decode(bytes);
  } catch {
    return '';
  }
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// Keyword sets per language (small, curated).
const KEYWORDS: Record<string, string[]> = {
  go: ['package', 'import', 'func', 'return', 'var', 'const', 'type', 'struct', 'interface', 'if', 'else', 'for', 'range', 'switch', 'case', 'default', 'go', 'defer', 'chan', 'map', 'nil', 'true', 'false'],
  javascript: ['const', 'let', 'var', 'function', 'return', 'if', 'else', 'for', 'while', 'switch', 'case', 'default', 'new', 'class', 'extends', 'import', 'from', 'export', 'async', 'await', 'try', 'catch', 'finally', 'throw', 'typeof', 'instanceof', 'null', 'undefined', 'true', 'false', 'this'],
  typescript: ['const', 'let', 'var', 'function', 'return', 'if', 'else', 'for', 'while', 'switch', 'case', 'default', 'new', 'class', 'extends', 'import', 'from', 'export', 'async', 'await', 'try', 'catch', 'finally', 'throw', 'interface', 'type', 'enum', 'implements', 'public', 'private', 'readonly', 'null', 'undefined', 'true', 'false', 'this', 'namespace'],
  python: ['def', 'return', 'if', 'elif', 'else', 'for', 'while', 'import', 'from', 'as', 'class', 'try', 'except', 'finally', 'with', 'lambda', 'pass', 'break', 'continue', 'True', 'False', 'None', 'and', 'or', 'not', 'in', 'is', 'yield', 'raise', 'global', 'async', 'await'],
  ruby: ['def', 'end', 'if', 'elsif', 'else', 'unless', 'while', 'do', 'class', 'module', 'require', 'include', 'attr', 'return', 'yield', 'begin', 'rescue', 'ensure', 'nil', 'true', 'false', 'self'],
  php: ['function', 'return', 'if', 'elseif', 'else', 'foreach', 'for', 'while', 'class', 'public', 'private', 'protected', 'static', 'new', 'echo', 'require', 'include', 'null', 'true', 'false', 'use', 'namespace'],
  rust: ['fn', 'let', 'mut', 'pub', 'struct', 'enum', 'impl', 'trait', 'match', 'if', 'else', 'for', 'while', 'loop', 'return', 'use', 'mod', 'self', 'Self', 'true', 'false', 'const', 'static'],
  java: ['public', 'private', 'protected', 'class', 'interface', 'static', 'final', 'void', 'int', 'new', 'return', 'if', 'else', 'for', 'while', 'switch', 'case', 'default', 'import', 'package', 'try', 'catch', 'finally', 'null', 'true', 'false', 'this', 'extends'],
  c: ['int', 'char', 'float', 'double', 'void', 'struct', 'typedef', 'if', 'else', 'for', 'while', 'switch', 'case', 'default', 'return', 'include', 'define', 'static', 'const', 'sizeof'],
  cpp: ['int', 'char', 'float', 'double', 'void', 'struct', 'class', 'public', 'private', 'template', 'typename', 'if', 'else', 'for', 'while', 'switch', 'case', 'return', 'include', 'define', 'new', 'delete', 'namespace', 'using'],
  csharp: ['public', 'private', 'protected', 'class', 'interface', 'static', 'void', 'new', 'return', 'if', 'else', 'for', 'foreach', 'while', 'switch', 'case', 'using', 'namespace', 'try', 'catch', 'null', 'true', 'false', 'var'],
  bash: ['if', 'then', 'else', 'elif', 'fi', 'for', 'in', 'do', 'done', 'while', 'case', 'esac', 'function', 'echo', 'export', 'local', 'return', 'exit'],
  json: [],
  xml: [],
  yaml: [],
  toml: [],
  markdown: [],
  csv: [],
  sql: ['select', 'from', 'where', 'insert', 'into', 'values', 'update', 'set', 'delete', 'create', 'table', 'join', 'left', 'right', 'inner', 'outer', 'on', 'group', 'by', 'order', 'limit', 'offset', 'and', 'or', 'not', 'null', 'as'],
};

function highlightLine(line: string, lang: string): string {
  const escaped = escapeHtml(line);
  if (lang === 'markdown') {
    return escaped
      .replace(/^(#+.*)$/g, '<span style="color:var(--accent);font-weight:700">$1</span>')
      .replace(/`([^`]+)`/g, '<span style="color:var(--warning)">$1</span>');
  }
  if (lang === 'csv') {
    return escaped.replace(/([^,]+)/g, '<span style="color:var(--text)">$1</span>');
  }
  // Comments.
  let out = escaped;
  if (['go', 'javascript', 'typescript', 'java', 'c', 'cpp', 'csharp', 'rust'].includes(lang)) {
    out = out.replace(/(\/\/.*$)/g, '<span style="color:var(--text-faint);font-style:italic">$1</span>');
  }
  if (lang === 'python' || lang === 'ruby' || lang === 'bash' || lang === 'php') {
    out = out.replace(/(#.*$)/g, '<span style="color:var(--text-faint);font-style:italic">$1</span>');
  }
  if (lang === 'sql') {
    out = out.replace(/(--.*$)/g, '<span style="color:var(--text-faint);font-style:italic">$1</span>');
  }
  // Strings.
  out = out.replace(/(&quot;.*?&quot;|&#39;.*?&#39;|`[^`]*`)/g, '<span style="color:var(--success)">$1</span>');
  // Keywords (word boundary, only outside spans - approximate but safe).
  const kws = KEYWORDS[lang] ?? [];
  if (kws.length) {
    const re = new RegExp(`\\b(${kws.map(escapeRegExp).join('|')})\\b`, 'g');
    out = out.replace(re, (m) => `<span style="color:var(--accent);font-weight:600">${m}</span>`);
  }
  // Numbers.
  out = out.replace(/\b(\d+(?:\.\d+)?)\b/g, '<span style="color:var(--warning)">$1</span>');
  return out;
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

export function highlight(code: string, lang: string): string {
  const language = KEYWORDS[lang] ? lang : '';
  return code
    .split('\n')
    .map((line) => (language ? highlightLine(line, language) : escapeHtml(line)))
    .join('\n');
}
