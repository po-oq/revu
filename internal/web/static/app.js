const AUTHOR_KEY = "revu.mock.author.v1";
const DEVICE_KEY = "revu.device.v1";
const HISTORY_VIEW_KEY = "revu.historyDiffView.v1";
const API_BASE = "/api";
const app = document.querySelector("#app");
const blobStore = new Map();

const state = {
  threads: [],
  comments: [],
  currentAuthor: "名無し",
  selectedThreadId: null,
  view: "home",
  createType: "markdown",
  commentsCollapsed: false,
  drafts: {},
  errors: [],
  isHost: false,
  history: null
};

function ensureDeviceId() {
  try {
    const existing = localStorage.getItem(DEVICE_KEY);
    if (/^dev_[0-9a-f]{24}$/.test(existing || "")) return existing;
    const bytes = new Uint8Array(12);
    crypto.getRandomValues(bytes);
    const id = `dev_${Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("")}`;
    localStorage.setItem(DEVICE_KEY, id);
    return id;
  } catch (error) {
    const bytes = new Uint8Array(12);
    crypto.getRandomValues(bytes);
    return `dev_${Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("")}`;
  }
}

function currentIdentity() {
  return {
    deviceId: ensureDeviceId(),
    authorName: state.currentAuthor || "名無し"
  };
}

function displayDeviceMark(deviceId) {
  const value = String(deviceId || "");
  const marker = value.startsWith("dev_") ? value.slice(4) : value;
  return `◆${marker.slice(0, 6).toUpperCase()}`;
}

function authorLabel(item = {}) {
  return `${item.authorName || item.author || "名無し"} ${displayDeviceMark(item.ownerDeviceId)}`;
}

function isOwnItem(item = {}) {
  return item.ownerDeviceId === ensureDeviceId();
}

// 削除はホストも可、編集は本人のみ。isHost は表示制御用で、権限の強制はサーバ側で行う。
function canDeleteItem(item = {}) {
  return isOwnItem(item) || state.isHost;
}

function normalizeAttachmentFromAPI(attachment = {}) {
  return {
    ...attachment,
    type: attachment.type || attachment.mimeType || "application/octet-stream"
  };
}

function normalizeCommentFromAPI(comment = {}) {
  const attachments = (comment.attachments || []).map(normalizeAttachmentFromAPI);
  return {
    ...comment,
    threadId: comment.threadId,
    number: Number(comment.number || 0),
    author: comment.authorName || comment.author || "名無し",
    body: comment.body || "",
    attachments,
    createdAt: comment.createdAt || nowIso(),
    threadVersion: Number(comment.threadVersion || 0)
  };
}

function normalizeThreadFromAPI(thread = {}) {
  const attachments = (thread.attachments || []).map(normalizeAttachmentFromAPI);
  const comments = (thread.comments || []).map(normalizeCommentFromAPI);
  const createdBy = authorLabel(thread);
  const latestActor = thread.latestActor && thread.latestActor !== thread.authorName
    ? thread.latestActor
    : createdBy;
  return {
    ...thread,
    type: thread.type || "text",
    title: thread.title || "",
    body: thread.body || "",
    sourceFileMeta: null,
    fileAttachment: attachments[0] || null,
    attachments,
    comments,
    commentCount: Number(thread.commentCount ?? comments.length),
    currentVersion: Number(thread.currentVersion || 1),
    createdBy,
    updatedBy: latestActor,
    latestActor,
    createdAt: thread.createdAt || nowIso(),
    updatedAt: thread.updatedAt || thread.latestAt || thread.createdAt || nowIso(),
    latestAt: thread.latestAt || thread.updatedAt || thread.createdAt || nowIso()
  };
}

function apiErrorMessage(error, fallback = "サーバーとの通信に失敗しました。") {
  return error?.message || fallback;
}

async function apiJson(path, options = {}) {
  const headers = {
    Accept: "application/json",
    ...(options.headers || {})
  };
  const init = { ...options, headers };
  if (Object.prototype.hasOwnProperty.call(options, "body")) {
    const isFormData = typeof FormData !== "undefined" && options.body instanceof FormData;
    if (!isFormData && typeof options.body !== "string") {
      init.body = JSON.stringify(options.body);
      init.headers = { "Content-Type": "application/json", ...headers };
    }
  }

  const response = await fetch(`${API_BASE}${path}`, init);
  const contentType = response.headers.get("content-type") || "";
  const payload = contentType.includes("application/json") ? await response.json() : null;
  if (!response.ok) {
    throw new Error(payload?.error || `HTTP ${response.status}`);
  }
  return payload;
}

async function uploadFile(file) {
  const formData = new FormData();
  formData.append("file", file, file.name || "upload");
  const response = await fetch(`${API_BASE}/uploads`, {
    method: "POST",
    headers: { Accept: "application/json" },
    body: formData
  });
  const contentType = response.headers.get("content-type") || "";
  const payload = contentType.includes("application/json") ? await response.json() : null;
  if (!response.ok) {
    throw new Error(payload?.error || `HTTP ${response.status}`);
  }
  return normalizeAttachmentFromAPI(payload || {});
}

async function refreshMe() {
  try {
    const me = await apiJson("/me");
    state.isHost = Boolean(me?.isHost);
  } catch (error) {
    state.isHost = false;
  }
}

async function refreshThreads() {
  const threads = await apiJson("/threads");
  state.threads = (threads || []).map(normalizeThreadFromAPI);
  const threadIds = new Set(state.threads.map((thread) => thread.id));
  state.comments = state.comments.filter((comment) => threadIds.has(comment.threadId));
}

async function refreshThread(threadId) {
  const thread = normalizeThreadFromAPI(await apiJson(`/threads/${encodeURIComponent(threadId)}`));
  const index = state.threads.findIndex((item) => item.id === thread.id);
  if (index >= 0) {
    state.threads[index] = thread;
  } else {
    state.threads.unshift(thread);
  }
  state.comments = state.comments
    .filter((comment) => comment.threadId !== thread.id)
    .concat(thread.comments || []);
  return thread;
}

function serverAttachmentIds(attachments) {
  return (attachments || [])
    .map((attachment) => attachment.serverId || attachment.id)
    .filter((attachmentId) => /^att_/.test(String(attachmentId || "")));
}

function nowIso() {
  return new Date().toISOString();
}

function id(prefix) {
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

function formatDate(iso) {
  return new Intl.DateTimeFormat("ja-JP", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  }).format(new Date(iso));
}

function formatDateFull(iso) {
  return new Intl.DateTimeFormat("ja-JP", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  }).format(new Date(iso));
}

function formatRelative(iso) {
  const time = new Date(iso).getTime();
  if (!Number.isFinite(time)) return formatDate(iso);
  const diff = Date.now() - time;
  if (diff < 45 * 1000) return "たった今";
  const minutes = Math.floor(diff / 60000);
  if (minutes < 60) return `${minutes}分前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}時間前`;
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days}日前`;
  return formatDate(iso);
}

// 相対時刻＋ホバー(title)で絶対時刻を出すHTMLを返す
function timeLabel(iso) {
  return `<span class="time" title="${escapeHtml(formatDateFull(iso))}">${escapeHtml(formatRelative(iso))}</span>`;
}

// 投稿者名から決定的に識別色を割り当てる（和色4色 / 「名無し」は無色）
const AUTHOR_PALETTE = ["shu", "ai", "tokiwa", "sumire"];
function authorColorKey(author) {
  const name = String(author || "").trim();
  if (!name || name === "名無し") return "none";
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = (hash * 31 + name.charCodeAt(i)) >>> 0;
  }
  return AUTHOR_PALETTE[hash % AUTHOR_PALETTE.length];
}

function formatBytes(size) {
  if (!Number.isFinite(size)) return "-";
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function typeIcon(type) {
  return {
    markdown: "MD",
    html: "HTML",
    text: "TXT",
    file: "FILE"
  }[type] || "DOC";
}

function ensureDraft(type = state.createType) {
  if (!state.drafts[type]) {
    state.drafts[type] = {
      title: "",
      body: "",
      fileMeta: null,
      fileAttachment: null
    };
  }
  return state.drafts[type];
}

function setCreateType(type) {
  state.createType = type;
  ensureDraft(type);
  renderApp();
}

function updateDraftField(field, value) {
  const draft = ensureDraft();
  draft[field] = value;
}

function ensureCommentDraft(threadId = state.selectedThreadId) {
  const key = `comment:${threadId}`;
  if (!state.drafts[key]) {
    state.drafts[key] = { body: "", attachments: [] };
  }
  return state.drafts[key];
}

function formatQuote(text) {
  const trimmed = String(text || "").replace(/\r\n?/g, "\n").replace(/\s+$/, "");
  if (!trimmed) return "";
  return trimmed.split("\n").map((line) => `> ${line}`).join("\n");
}

// 表示・入力同期はすべて選択中スレの下書きに紐づくため、threadId は引数に取らない
function appendQuoteToCommentDraft(text) {
  const quote = formatQuote(text);
  if (!quote) return;
  const draft = ensureCommentDraft();
  const base = draft.body.replace(/\s+$/, "");
  draft.body = base ? `${base}\n\n${quote}\n\n` : `${quote}\n\n`;
  state.commentsCollapsed = false;
  renderApp();
  const input = app.querySelector("[data-comment-body]");
  if (input) {
    input.focus();
    input.selectionStart = input.selectionEnd = input.value.length;
    input.scrollTop = input.scrollHeight;
  }
}

function nextCommentNumber(threadId) {
  const comments = commentsForThread(threadId);
  return comments.length ? Math.max(...comments.map((comment) => comment.number)) + 1 : 1;
}

function fileToMeta(file, ownerKind = "draft", ownerId = "draft") {
  return {
    id: id("att"),
    name: file.name || "pasted-file",
    size: file.size,
    type: file.type || "application/octet-stream",
    createdAt: nowIso(),
    ownerKind,
    ownerId
  };
}

function safeDomId(value) {
  return String(value).replace(/[^A-Za-z0-9_-]/g, "-");
}

function readTextFile(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(reader.error || new Error("file read failed"));
    reader.readAsText(file);
  });
}

function isTextPreviewable(meta) {
  const name = meta.name.toLowerCase();
  return (meta.type || "").startsWith("text/") ||
    [".txt", ".md", ".html", ".css", ".js", ".json"].some((suffix) => name.endsWith(suffix));
}

function releaseAttachment(meta) {
  if (!meta) return;
  const blob = blobStore.get(meta.id);
  if (blob?.url) URL.revokeObjectURL(blob.url);
  blobStore.delete(meta.id);
}

async function deleteThread() {
  const thread = state.threads.find((item) => item.id === state.selectedThreadId);
  if (!thread) return;
  if (!canDeleteItem(thread)) {
    pushError("自分の端末で作成したスレッドだけ削除できます。");
    renderApp();
    return;
  }
  const count = commentsForThread(thread.id).length;
  const hostNote = isOwnItem(thread) ? "" : "ホスト権限で削除します。\n";
  const ok = confirm(`${hostNote}スレ「${thread.title}」を削除しますか？\nコメント${count}件と添付ファイルも一緒に削除されます。この操作は元に戻せません。`);
  if (!ok) return;
  try {
    await apiJson(`/threads/${encodeURIComponent(thread.id)}`, {
      method: "DELETE",
      body: currentIdentity()
    });
    for (const comment of commentsForThread(thread.id)) {
      (comment.attachments || []).forEach(releaseAttachment);
    }
    releaseAttachment(thread.fileAttachment);
    state.comments = state.comments.filter((comment) => comment.threadId !== thread.id);
    state.threads = state.threads.filter((item) => item.id !== thread.id);
    delete state.drafts[`comment:${thread.id}`];
    await refreshThreads();
    clearErrors();
    goHome();
  } catch (error) {
    pushError(apiErrorMessage(error, "スレッドを削除できませんでした。"));
    renderApp();
  }
}

async function deleteComment(commentId) {
  const comment = state.comments.find((item) => item.id === commentId);
  if (!comment) return;
  if (!canDeleteItem(comment)) {
    pushError("自分の端末で投稿したコメントだけ削除できます。");
    renderApp();
    return;
  }
  // 番号は詰めない: >>n アンカーが別コメントを指してしまうのを防ぐため欠番として維持する
  const hostNote = isOwnItem(comment) ? "" : "ホスト権限で削除します。\n";
  const ok = confirm(`${hostNote}コメント #${comment.number}（${authorLabel(comment)}）を削除しますか？`);
  if (!ok) return;
  try {
    await apiJson(`/comments/${encodeURIComponent(commentId)}`, {
      method: "DELETE",
      body: currentIdentity()
    });
    (comment.attachments || []).forEach(releaseAttachment);
    if (state.selectedThreadId) await refreshThread(state.selectedThreadId);
    clearErrors();
    renderApp();
  } catch (error) {
    pushError(apiErrorMessage(error, "コメントを削除できませんでした。"));
    renderApp();
  }
}

function clearDraftFileAttachment(draft) {
  const attachment = draft.fileAttachment;
  if (!attachment || attachment.ownerKind !== "draft") return;
  const stored = blobStore.get(attachment.id);
  if (stored?.url) URL.revokeObjectURL(stored.url);
  blobStore.delete(attachment.id);
  draft.fileAttachment = null;
}

async function handleCreateFiles(files) {
  clearErrors();
  const file = Array.from(files)[0];
  if (!file) return;
  const draft = ensureDraft();

  if (state.createType === "file") {
    try {
      const attachment = await uploadFile(file);
      clearDraftFileAttachment(draft);
      draft.fileAttachment = attachment;
      draft.title = draft.title || file.name;
      clearErrors();
      renderApp();
    } catch (error) {
      pushError(apiErrorMessage(error, `${file.name || "ファイル"}をアップロードできませんでした。`));
      renderApp();
    }
    return;
  }

  try {
    const text = await readTextFile(file);
    draft.body = text;
    draft.title = draft.title || file.name.replace(/\.[^.]+$/, "");
    draft.fileMeta = fileToMeta(file);
    renderApp();
  } catch (error) {
    pushError("ファイルを読み込めませんでした。");
    renderApp();
  }
}

async function addCommentAttachments(files) {
  clearErrors();
  const draft = ensureCommentDraft();
  const failures = [];
  for (const file of Array.from(files)) {
    try {
      draft.attachments.push(await uploadFile(file));
    } catch (error) {
      failures.push(`${file.name || "ファイル"}: ${apiErrorMessage(error, "アップロードできませんでした。")}`);
    }
  }
  if (failures.length) pushError(failures.join(" / "));
  renderApp();
}

function removeDraftAttachment(attachmentId) {
  const draft = ensureCommentDraft();
  draft.attachments = draft.attachments.filter((attachment) => attachment.id !== attachmentId);
  const blob = blobStore.get(attachmentId);
  if (blob) URL.revokeObjectURL(blob.url);
  blobStore.delete(attachmentId);
  renderApp();
}

async function postComment() {
  const thread = state.threads.find((item) => item.id === state.selectedThreadId);
  if (!thread) return;
  clearErrors();
  const draft = ensureCommentDraft(thread.id);
  const body = draft.body.trim();
  if (!body && draft.attachments.length === 0) {
    pushError("コメント本文または添付を追加してください。");
    renderApp();
    return;
  }
  try {
    await apiJson(`/threads/${encodeURIComponent(thread.id)}/comments`, {
      method: "POST",
      body: {
        body,
        attachmentIds: serverAttachmentIds(draft.attachments),
        ...currentIdentity()
      }
    });
    state.drafts[`comment:${thread.id}`] = { body: "", attachments: [] };
    await refreshThread(thread.id);
    clearErrors();
    renderApp();
  } catch (error) {
    pushError(apiErrorMessage(error, "コメントを投稿できませんでした。"));
    renderApp();
  }
}

async function createThreadFromDraft() {
  clearErrors();
  const type = state.createType;
  const draft = ensureDraft(type);
  const title = draft.title.trim();
  if (!title) {
    pushError("スレタイを入力してください。");
    renderApp();
    return;
  }
  if (type !== "file" && !draft.body.trim()) {
    pushError("本文を入力するかファイルをアップロードしてください。");
    renderApp();
    return;
  }
  if (type === "file" && !draft.fileAttachment) {
    pushError("ファイルをアップロードしてください。");
    renderApp();
    return;
  }

  try {
    const created = normalizeThreadFromAPI(await apiJson("/threads", {
      method: "POST",
      body: {
        type,
        title,
        body: type === "file" ? "" : draft.body,
        attachmentIds: serverAttachmentIds(draft.fileAttachment ? [draft.fileAttachment] : []),
        ...currentIdentity()
      }
    }));
    clearDraftFileAttachment(draft);
    state.drafts[type] = { title: "", body: "", fileMeta: null, fileAttachment: null };
    await refreshThreads();
    state.view = "thread";
    state.selectedThreadId = created.id;
    state.commentsCollapsed = false;
    await refreshThread(created.id);
    clearErrors();
    renderApp();
  } catch (error) {
    pushError(apiErrorMessage(error, "スレッドを作成できませんでした。"));
    renderApp();
  }
}

function createSampleState() {
  const createdAt = "2026-07-03T06:00:00.000Z";
  const markdownBody = `# Markdown仕様レビュー

- トップはスレ一覧
- 本文とコメントは並列
- Mermaidはpan/zoomできる

\`\`\`mermaid
flowchart LR
  A[Thread list] --> B[Create thread]
  B --> C[Thread detail]
  C --> D[Comments]
\`\`\`
`;

  const htmlBody = `<style>
body { font-family: system-ui, sans-serif; padding: 24px; color: #172033; }
.note { border: 1px solid #bcd7f5; background: #eef7ff; padding: 14px; border-radius: 8px; }
button { padding: 8px 12px; border-radius: 6px; border: 1px solid #9ab; background: white; }
</style>
<main>
  <h1>HTML仕様メモ</h1>
  <p class="note">HTMLをそのままiframe srcdocへ表示するサンプルです。</p>
  <button onclick="document.body.dataset.clicked = 'true'; this.textContent = 'clicked';">sandbox JS</button>
</main>`;

  const textBody = `これは長めのプレーンテキストサンプルです。

HTMLとして解釈せず、改行と空白を読みやすく保って表示します。
コメント欄を折り畳んだとき、本文側が広くなることを確認します。`;

  const threads = [
    {
      id: "thread-md-sample",
      type: "markdown",
      title: "Markdown仕様レビュー",
      body: markdownBody,
      sourceFileMeta: { name: "spec-review.md", size: markdownBody.length, type: "text/markdown" },
      fileAttachment: null,
      createdBy: "wata",
      updatedBy: "名無し",
      createdAt,
      updatedAt: "2026-07-03T06:20:00.000Z"
    },
    {
      id: "thread-html-sample",
      type: "html",
      title: "HTMLで書いた仕様メモ",
      body: htmlBody,
      sourceFileMeta: { name: "layout-note.html", size: htmlBody.length, type: "text/html" },
      fileAttachment: null,
      createdBy: "wata",
      updatedBy: "guest",
      createdAt: "2026-07-03T06:04:00.000Z",
      updatedAt: "2026-07-03T06:18:00.000Z"
    },
    {
      id: "thread-text-sample",
      type: "text",
      title: "プレーンテキスト議事メモ",
      body: textBody,
      sourceFileMeta: { name: "meeting.txt", size: textBody.length, type: "text/plain" },
      fileAttachment: null,
      createdBy: "名無し",
      updatedBy: "名無し",
      createdAt: "2026-07-03T06:08:00.000Z",
      updatedAt: "2026-07-03T06:08:00.000Z"
    },
    {
      id: "thread-file-sample",
      type: "file",
      title: "添付ファイル確認スレ",
      body: "",
      sourceFileMeta: null,
      fileAttachment: {
        id: "file-sample-1",
        name: "sample-archive.zip",
        size: 1048576,
        type: "application/zip",
        createdAt: "2026-07-03T06:12:00.000Z",
        ownerKind: "thread",
        ownerId: "thread-file-sample"
      },
      createdBy: "guest",
      updatedBy: "名無し",
      createdAt: "2026-07-03T06:12:00.000Z",
      updatedAt: "2026-07-03T06:16:00.000Z"
    }
  ];

  const comments = [
    {
      id: "comment-md-1",
      threadId: "thread-md-sample",
      number: 1,
      author: "名無し",
      body: "Mermaidのpan/zoomを本文内で確認したい。",
      attachments: [],
      createdAt: "2026-07-03T06:10:00.000Z"
    },
    {
      id: "comment-md-2",
      threadId: "thread-md-sample",
      number: 2,
      author: "wata",
      body: ">>1 スクショ添付もコメント下に出る想定です。",
      attachments: [
        {
          id: "att-sample-1",
          name: "screenshot-sample.png",
          size: 245760,
          type: "image/png",
          createdAt: "2026-07-03T06:12:00.000Z",
          ownerKind: "comment",
          ownerId: "comment-md-2"
        }
      ],
      createdAt: "2026-07-03T06:12:00.000Z"
    },
    {
      id: "comment-html-1",
      threadId: "thread-html-sample",
      number: 1,
      author: "guest",
      body: "HTMLはCSS込みで貼り付ければ十分そう。",
      attachments: [],
      createdAt: "2026-07-03T06:18:00.000Z"
    },
    {
      id: "comment-file-1",
      threadId: "thread-file-sample",
      number: 1,
      author: "名無し",
      body: "リロード後は実体なし表示になることを確認する。",
      attachments: [],
      createdAt: "2026-07-03T06:16:00.000Z"
    }
  ];

  return { threads, comments };
}

function saveState() {
  try {
    localStorage.setItem(AUTHOR_KEY, state.currentAuthor);
  } catch (error) {
    pushError("投稿者名を保存できませんでした。");
  }
}

async function loadState() {
  try {
    state.currentAuthor = localStorage.getItem(AUTHOR_KEY) || "名無し";
    ensureDeviceId();
  } catch (error) {
    state.currentAuthor = "名無し";
    pushError("投稿者名を読み込めなかったため、名無しで表示しています。");
  }
  await refreshMe();
  await refreshThreads();
}

function pushError(message) {
  state.errors = [message];
}

function clearErrors() {
  state.errors = [];
}

function commentsForThread(threadId) {
  return state.comments
    .filter((comment) => comment.threadId === threadId)
    .sort((a, b) => a.number - b.number);
}

function latestActor(thread) {
  const comments = commentsForThread(thread.id);
  const latestComment = comments.at(-1);
  return latestComment ? authorLabel(latestComment) : thread.latestActor || thread.updatedBy || authorLabel(thread);
}

function latestTime(thread) {
  const comments = commentsForThread(thread.id);
  const latestComment = comments.at(-1);
  return latestComment ? latestComment.createdAt : thread.latestAt || thread.updatedAt || thread.createdAt;
}

function goHome() {
  state.view = "home";
  state.selectedThreadId = null;
  renderApp();
}

function goCreate(type = "markdown") {
  state.view = "create";
  state.createType = type;
  state.selectedThreadId = null;
  renderApp();
}

async function goThread(threadId) {
  state.view = "thread";
  state.selectedThreadId = threadId;
  state.commentsCollapsed = false;
  renderApp();
  try {
    await refreshThread(threadId);
    clearErrors();
    renderApp();
  } catch (error) {
    pushError(apiErrorMessage(error, "スレッドを読み込めませんでした。"));
    renderApp();
  }
}

function editAuthor() {
  const next = prompt("名前を入力", state.currentAuthor);
  if (next === null) return;
  const trimmed = next.trim();
  state.currentAuthor = trimmed || "名無し";
  saveState();
  renderApp();
}

function startEditThread() {
  const thread = state.threads.find((item) => item.id === state.selectedThreadId);
  if (!thread) {
    pushError("編集対象のスレが見つかりません。");
    renderApp();
    return;
  }
  if (!isOwnItem(thread)) {
    pushError("自分の端末で作成したスレッドだけ編集できます。");
    renderApp();
    return;
  }
  state.view = "edit";
  state.drafts.edit = {
    threadId: thread.id,
    title: thread.title,
    body: thread.body
  };
  clearErrors();
  renderApp();
}

async function saveThreadEdit() {
  const draft = state.drafts.edit;
  const thread = state.threads.find((item) => item.id === draft?.threadId);
  if (!thread) {
    pushError("編集対象のスレが見つかりません。");
    renderApp();
    return;
  }
  if (!isOwnItem(thread)) {
    pushError("自分の端末で作成したスレッドだけ編集できます。");
    renderApp();
    return;
  }
  const title = (draft.title || "").trim();
  if (!title) {
    pushError("スレタイを入力してください。");
    renderApp();
    return;
  }
  try {
    await apiJson(`/threads/${encodeURIComponent(thread.id)}`, {
      method: "PUT",
      body: {
        title,
        body: thread.type === "file" ? thread.body : draft.body,
        ...currentIdentity()
      }
    });
    state.view = "thread";
    state.selectedThreadId = thread.id;
    await refreshThread(thread.id);
    clearErrors();
    renderApp();
  } catch (error) {
    pushError(apiErrorMessage(error, "スレッドを保存できませんでした。"));
    renderApp();
  }
}

function loadHistoryViewMode() {
  return localStorage.getItem(HISTORY_VIEW_KEY) === "split" ? "split" : "unified";
}

function setHistoryViewMode(mode) {
  if (!state.history || (mode !== "unified" && mode !== "split")) return;
  state.history.viewMode = mode;
  localStorage.setItem(HISTORY_VIEW_KEY, mode);
  renderApp();
}

async function goHistory(threadId, initialSeq = null) {
  state.view = "history";
  state.selectedThreadId = threadId;
  state.history = {
    threadId,
    versions: [],
    selectedSeq: null,
    diff: null,
    loading: true,
    diffLoading: false,
    error: null,
    fallbackSide: "new",
    viewMode: loadHistoryViewMode()
  };
  renderApp();
  try {
    const payload = await apiJson(`/threads/${encodeURIComponent(threadId)}/edits`);
    if (state.view !== "history" || state.history?.threadId !== threadId) return;
    state.history.versions = payload?.versions || [];
    state.history.loading = false;
    clearErrors();
    renderApp();
    if (state.history.versions.length > 1) {
      const target = state.history.versions.some((version) => version.seq === initialSeq)
        ? initialSeq
        : state.history.versions[0].seq;
      await selectHistoryVersion(target);
    }
  } catch (error) {
    if (state.view !== "history" || state.history?.threadId !== threadId) return;
    state.history.loading = false;
    state.history.error = apiErrorMessage(error, "編集履歴を読み込めませんでした。");
    renderApp();
  }
}

async function selectHistoryVersion(seq) {
  if (!state.history) return;
  state.history.selectedSeq = seq;
  state.history.diff = null;
  state.history.diffLoading = true;
  state.history.error = null;
  state.history.fallbackSide = "new";
  renderApp();
  try {
    const diff = await apiJson(`/threads/${encodeURIComponent(state.history.threadId)}/edits/${encodeURIComponent(seq)}/diff`);
    if (state.view !== "history" || !state.history || state.history.selectedSeq !== seq) return;
    state.history.diff = diff;
    state.history.diffLoading = false;
    renderApp();
  } catch (error) {
    if (state.view !== "history" || !state.history || state.history.selectedSeq !== seq) return;
    state.history.diffLoading = false;
    state.history.error = apiErrorMessage(error, "差分を読み込めませんでした。");
    renderApp();
  }
}

function renderApp() {
  hideQuotePopup();
  app.innerHTML = `
    <header class="topbar">
      <button class="brand" data-action="home"><span class="brand-mark">R</span>revu</button>
      <div class="top-actions">
        <button class="primary" data-action="create">＋ 新規スレ</button>
        <button class="author-button" data-action="edit-author" title="投稿者名を変更"><span class="avatar-dot" data-author-color="${authorColorKey(state.currentAuthor)}"></span>${escapeHtml(state.currentAuthor)}</button>
      </div>
    </header>
    <main class="workspace">
      ${state.errors.map((error) => `<div class="error">${escapeHtml(error)}</div>`).join("")}
      ${renderCurrentView()}
    </main>
  `;
  const selected = state.threads.find((thread) => thread.id === state.selectedThreadId);
  const markdownContainer = app.querySelector("[data-rendered-markdown]");
  if (state.view === "create" && state.createType === "markdown" && markdownContainer) {
    hydrateMermaid(markdownContainer, ensureDraft().body || "# Markdown preview");
  } else if (state.view === "thread" && selected && selected.type === "markdown" && markdownContainer) {
    hydrateMermaid(markdownContainer, selected.body);
  } else if (state.view === "edit" && markdownContainer) {
    const draft = state.drafts.edit;
    const thread = state.threads.find((item) => item.id === draft?.threadId);
    if (thread?.type === "markdown") hydrateMermaid(markdownContainer, draft.body);
  }
}

function renderCurrentView() {
  if (state.view === "create") return renderCreateView();
  if (state.view === "edit") return renderEditView();
  if (state.view === "thread") return renderThreadView();
  if (state.view === "history") return renderHistoryView();
  return renderHomeView();
}

function renderHomeView() {
  const rows = state.threads
    .slice()
    .sort((a, b) => new Date(latestTime(b)) - new Date(latestTime(a)))
    .map((thread) => {
      const commentCount = Number.isFinite(thread.commentCount) ? thread.commentCount : commentsForThread(thread.id).length;
      return `
        <button class="thread-row" data-action="open-thread" data-thread-id="${escapeHtml(thread.id)}">
          <span class="type-badge" data-type="${escapeHtml(thread.type)}">${escapeHtml(typeIcon(thread.type))}</span>
          <span class="thread-row-main">
            <span class="thread-row-title">${escapeHtml(thread.title)}</span>
            <span class="thread-row-meta">${escapeHtml(authorLabel(thread))} / ${escapeHtml(latestActor(thread))} ・ ${timeLabel(latestTime(thread))}</span>
          </span>
          <span class="count-pill">${commentCount}</span>
        </button>
      `;
    })
    .join("");

  return `
    <section class="section-head">
      <div>
        <h1>スレッド一覧</h1>
        <div class="muted">レビュー対象を共有し、プレビューを見ながらコメントできます。</div>
      </div>
    </section>
    <section class="panel">
      <div class="thread-list">
        ${rows || `<div class="empty" style="border:0; border-radius:0;">まだスレッドがありません。「＋ 新規スレ」から作成できます。</div>`}
      </div>
    </section>
  `;
}

function renderCreateView() {
  const draft = ensureDraft();
  const type = state.createType;
  const bodyEnabled = type !== "file";
  return `
    <section class="section-head">
      <div>
        <h1>新規スレッド</h1>
        <div class="muted">md/html/textはアップロード内容をtextareaへ読み込み、textareaの中身を本文にします。</div>
      </div>
      <button data-action="home">← 一覧へ</button>
    </section>
    <section class="panel" style="padding: 20px;">
      <div class="create-grid">
        <div class="form-stack">
          <div class="field">
            <label>スレタイ</label>
            <input class="input" data-field="title" value="${escapeHtml(draft.title)}" placeholder="スレタイ">
          </div>
          <div class="tabs" role="tablist">
            ${["markdown", "html", "text", "file"].map((item) => `
              <button class="tab" role="tab" aria-selected="${item === type}" data-action="set-create-type" data-type="${item}">
                ${escapeHtml(typeIcon(item))}
              </button>
            `).join("")}
          </div>
          <label class="drop-zone" data-drop="create">
            <input type="file" data-action="choose-create-file" style="display:none">
            <span>${escapeHtml(type)} ファイルをここにドロップ<br>またはクリックして選択</span>
          </label>
          ${draft.fileMeta ? `<div class="muted">読込済み: ${escapeHtml(draft.fileMeta.name)} (${formatBytes(draft.fileMeta.size)})</div>` : ""}
          ${draft.fileAttachment ? `<div class="muted">添付: ${escapeHtml(draft.fileAttachment.name)} (${formatBytes(draft.fileAttachment.size)})</div>` : ""}
          ${bodyEnabled ? `
            <div class="field">
              <label>直接入力</label>
              <textarea class="textarea" data-field="body" placeholder="ここに${escapeHtml(type)}を入力">${escapeHtml(draft.body)}</textarea>
            </div>
          ` : ""}
          <button class="primary" data-action="create-thread">スレッドを作成</button>
        </div>
        <div class="preview-frame">
          <div class="preview-label">プレビュー</div>
          <div class="preview-box">${renderDraftPreview(type, draft)}</div>
        </div>
      </div>
    </section>
  `;
}

function renderDraftPreview(type, draft) {
  if (type === "markdown") return renderMarkdown(draft.body || "# Markdown preview");
  if (type === "html") return renderHtmlFrame(draft.body || "<p>HTML preview</p>");
  if (type === "text") return renderText(draft.body || "Text preview");
  if (type === "file") return renderAttachmentPreview(draft.fileAttachment);
  return "";
}

function refreshCreatePreview() {
  const preview = app.querySelector(".preview-box");
  if (!preview || state.view !== "create") return;
  preview.innerHTML = renderDraftPreview(state.createType, ensureDraft());
  const markdownContainer = preview.querySelector("[data-rendered-markdown]");
  if (state.createType === "markdown" && markdownContainer) {
    hydrateMermaid(markdownContainer, ensureDraft().body || "# Markdown preview");
  }
}

function renderEditView() {
  const draft = state.drafts.edit;
  const thread = state.threads.find((item) => item.id === draft?.threadId);
  if (!thread) return `<div class="panel" style="padding:18px;">編集対象が見つかりません。</div>`;
  const bodyEnabled = thread.type !== "file";
  return `
    <section class="section-head">
      <div>
        <h1>スレッドを編集</h1>
        <div class="muted"><span class="type-badge" data-type="${escapeHtml(thread.type)}">${escapeHtml(typeIcon(thread.type))}</span></div>
      </div>
      <button data-action="open-thread" data-thread-id="${escapeHtml(thread.id)}">キャンセル</button>
    </section>
    <section class="panel" style="padding: 20px;">
      <div class="create-grid">
        <div class="form-stack">
          <div class="field">
            <label>スレタイ</label>
            <input class="input" data-edit-title value="${escapeHtml(draft.title)}" placeholder="スレタイ">
          </div>
          ${bodyEnabled ? `
            <div class="field">
              <label>本文</label>
              <textarea class="textarea" data-edit-body>${escapeHtml(draft.body)}</textarea>
            </div>
          ` : `
            <div class="muted">Fileスレはタイトルのみ編集できます。ファイルの差し替えはこのモックでは対象外です。</div>
          `}
          <button class="primary" data-action="save-thread-edit">保存する</button>
        </div>
        <div class="preview-frame">
          <div class="preview-label">プレビュー</div>
          <div class="preview-box">${renderDraftPreview(thread.type, { body: draft.body, fileAttachment: thread.fileAttachment })}</div>
        </div>
      </div>
    </section>
  `;
}

function refreshEditPreview() {
  const preview = app.querySelector(".preview-box");
  const draft = state.drafts.edit;
  const thread = state.threads.find((item) => item.id === draft?.threadId);
  if (!preview || state.view !== "edit" || !thread) return;
  preview.innerHTML = renderDraftPreview(thread.type, { body: draft.body, fileAttachment: thread.fileAttachment });
  const markdownContainer = preview.querySelector("[data-rendered-markdown]");
  if (thread.type === "markdown" && markdownContainer) {
    hydrateMermaid(markdownContainer, draft.body);
  }
}

let mermaidRenderSequence = 0;

function renderMarkdown(source) {
  if (!window.marked || !window.DOMPurify) {
    return `<div class="error">Markdownライブラリを読み込めませんでした。</div>`;
  }
  const mermaidBlocks = [];
  const replaced = String(source || "").replace(/```mermaid\s*([\s\S]*?)```/g, (_, code) => {
    const index = mermaidBlocks.push(code.trim()) - 1;
    return `<div data-mermaid-index="${index}"></div>`;
  });
  const html = DOMPurify.sanitize(marked.parse(replaced));
  return `<div class="content-view markdown-body" data-rendered-markdown>${html}</div>`;
}

async function hydrateMermaid(container, source) {
  const blocks = [];
  String(source || "").replace(/```mermaid\s*([\s\S]*?)```/g, (_, code) => {
    blocks.push(code.trim());
    return "";
  });
  const targets = container.querySelectorAll("[data-mermaid-index]");
  if (!window.mermaid || !window.svgPanZoom) {
    for (const target of targets) {
      target.innerHTML = `<div class="error">Mermaid図を描画できませんでした。</div>`;
    }
    return;
  }
  for (const target of targets) {
    const index = Number(target.dataset.mermaidIndex);
    const code = blocks[index];
    try {
      const renderId = `mermaid-${Date.now()}-${index}-${mermaidRenderSequence++}`;
      const result = await mermaid.render(renderId, code);
      // Mermaid runs with securityLevel: "strict". Do not sanitize the rendered SVG here:
      // DOMPurify strips Mermaid's HTML-based node labels in current Mermaid output.
      const svg = result.svg;
      target.innerHTML = `
        <div class="mermaid-shell">
          <div class="mermaid-toolbar">
            <button data-mermaid-action="zoom-in" title="拡大">＋</button>
            <button data-mermaid-action="zoom-out" title="縮小">−</button>
            <button data-mermaid-action="zoom-reset" title="図全体を表示">全体表示</button>
          </div>
          <div class="mermaid-canvas">${svg}</div>
        </div>
      `;
      const svgElement = target.querySelector(".mermaid-canvas svg");
      // svg-pan-zoom はコンテナいっぱいの SVG を前提とするため、
      // mermaid が付与する固定サイズや max-width を外して 100% に揃える
      svgElement.removeAttribute("width");
      svgElement.removeAttribute("height");
      svgElement.style.width = "100%";
      svgElement.style.height = "100%";
      svgElement.style.maxWidth = "none";
      // 図のアスペクト比に合わせてキャンバス高さを可変にする（横長図の下余白対策）
      const canvas = target.querySelector(".mermaid-canvas");
      const viewBox = svgElement.viewBox && svgElement.viewBox.baseVal;
      if (canvas && viewBox && viewBox.width > 0 && viewBox.height > 0) {
        const canvasWidth = canvas.clientWidth || 800;
        const ideal = canvasWidth * (viewBox.height / viewBox.width);
        const height = Math.max(240, Math.min(Math.ceil(ideal), 640));
        canvas.style.height = `${height}px`;
      }
      const panZoom = svgPanZoom(svgElement, {
        zoomEnabled: true,
        controlIconsEnabled: false,
        fit: true,
        center: true,
        minZoom: 0.2,
        maxZoom: 12,
        zoomScaleSensitivity: 0.3
      });
      const fitAll = () => {
        panZoom.resize();
        panZoom.fit();
        panZoom.center();
      };
      // レイアウト確定後にもう一度 fit しないと初期表示で図が見切れる
      requestAnimationFrame(fitAll);
      target.querySelector('[data-mermaid-action="zoom-in"]').addEventListener("click", (event) => {
        event.stopPropagation();
        panZoom.zoomIn();
      });
      target.querySelector('[data-mermaid-action="zoom-out"]').addEventListener("click", (event) => {
        event.stopPropagation();
        panZoom.zoomOut();
      });
      target.querySelector('[data-mermaid-action="zoom-reset"]').addEventListener("click", (event) => {
        event.stopPropagation();
        fitAll();
      });
    } catch (error) {
      target.innerHTML = `<div class="error">Mermaid図を描画できませんでした。</div>`;
    }
  }
}

let htmlFrameSequence = 0;

function renderHtmlFrame(source) {
  const frameId = `frame-${Date.now().toString(36)}-${htmlFrameSequence++}`;
  // sandbox に allow-same-origin を足すと投稿HTMLが親のlocalStorageへ
  // アクセスできてしまうため、postMessageで高さだけを親へ通知する方式にする。
  const probe = `<script>(function () {
    var post = function () {
      try {
        var body = document.body;
        if (!body) return;
        var rect = body.getBoundingClientRect();
        var marginBottom = parseFloat(getComputedStyle(body).marginBottom) || 0;
        parent.postMessage({
          type: "revu-frame-height",
          frameId: "${frameId}",
          height: Math.ceil(rect.bottom + marginBottom)
        }, "*");
      } catch (e) { /* noop */ }
    };
    if (window.ResizeObserver) {
      var observer = new ResizeObserver(post);
      observer.observe(document.documentElement);
      if (document.body) observer.observe(document.body);
    }
    window.addEventListener("load", post);
    setTimeout(post, 60);
    setTimeout(post, 400);
  })();<\/script>`;
  const doc = String(source || "") + probe;
  return `<iframe class="html-frame" title="HTMLプレビュー" data-frame-id="${escapeHtml(frameId)}" sandbox="allow-scripts allow-forms allow-popups" srcdoc="${escapeHtml(doc)}"></iframe>`;
}

function renderText(source) {
  return `<div class="content-view text-body">${escapeHtml(source || "")}</div>`;
}

function renderAttachmentPreview(meta) {
  if (!meta) return `<div class="empty">ファイルが選択されていません。</div>`;
  const type = meta.type || meta.mimeType || "application/octet-stream";
  const downloadUrl = meta.downloadUrl || (meta.id ? `${API_BASE}/attachments/${encodeURIComponent(meta.id)}/download` : "");
  if (downloadUrl) {
    if (type.startsWith("image/")) {
      return `
        <div class="attachment-preview">
          <img src="${escapeHtml(downloadUrl)}" alt="${escapeHtml(meta.name)}">
          <div><a href="${escapeHtml(downloadUrl)}" download="${escapeHtml(meta.name)}">ダウンロード</a></div>
        </div>
      `;
    }
    return `
      <div class="file-preview">
        <strong>${escapeHtml(meta.name)}</strong>
        <div class="muted">${escapeHtml(type)} / ${formatBytes(meta.size)}</div>
        <a href="${escapeHtml(downloadUrl)}" download="${escapeHtml(meta.name)}">ダウンロード</a>
      </div>
    `;
  }
  const blob = blobStore.get(meta.id);
  if (!blob) {
    return `
      <div class="file-preview">
        <strong>${escapeHtml(meta.name)}</strong>
        <div class="muted">${escapeHtml(type)} / ${formatBytes(meta.size)}</div>
        <p class="muted">このモックではリロード後にファイル実体を復元できません。</p>
      </div>
    `;
  }
  if (type.startsWith("image/")) {
    return `
      <div class="attachment-preview">
        <img src="${blob.url}" alt="${escapeHtml(meta.name)}">
        <div><a href="${blob.url}" download="${escapeHtml(meta.name)}">ダウンロード</a></div>
      </div>
    `;
  }
  if (isTextPreviewable(meta)) {
    const previewId = `text-preview-${safeDomId(meta.id)}`;
    const updatePreview = (text) => {
      const node = document.getElementById(previewId);
      if (node) node.textContent = text.slice(0, 4000);
    };
    if (!blob.textPromise) {
      blob.textPromise = blob.file.text().then((text) => {
        blob.text = text;
        return text;
      });
    }
    if (typeof blob.text === "string") {
      queueMicrotask(() => updatePreview(blob.text));
    } else {
      blob.textPromise.then(updatePreview);
    }
    return `
      <div class="attachment-preview">
        <strong>${escapeHtml(meta.name)}</strong>
        <div><a href="${blob.url}" download="${escapeHtml(meta.name)}">ダウンロード</a></div>
        <pre id="${escapeHtml(previewId)}" class="text-body">loading text preview...</pre>
      </div>
    `;
  }
  return `
    <div class="file-preview">
      <strong>${escapeHtml(meta.name)}</strong>
      <div class="muted">${escapeHtml(type)} / ${formatBytes(meta.size)}</div>
      <a href="${blob.url}" download="${escapeHtml(meta.name)}">ダウンロード</a>
    </div>
  `;
}

function renderThreadBody(thread) {
  if (!thread) return "";
  if (thread.type === "markdown") return renderMarkdown(thread.body);
  if (thread.type === "html") return renderHtmlFrame(thread.body);
  if (thread.type === "text") return renderText(thread.body);
  if (thread.type === "file") return renderAttachmentPreview(thread.fileAttachment);
  return "";
}

function renderAnchoredText(body) {
  return String(body || "").split("\n").map((line) => {
    const escaped = escapeHtml(line).replace(/&gt;&gt;(\d+)/g, '<span class="anchor">&gt;&gt;$1</span>');
    // 引用行は「> 」（スペース付き）始まり。「>>1」は「&gt;&gt;」になるので一致しない
    return /^&gt; /.test(escaped) ? `<span class="quote-line">${escaped}</span>` : escaped;
  }).join("\n");
}

function threadHasHistory(thread) {
  return thread.updatedAt !== thread.createdAt;
}

function versionBadge(thread, seq) {
  const version = Number(seq || 0);
  if (!version) return "";
  // リンク先ハッシュは起動時ディープリンク専用（boot時に一度だけ読まれる）
  if (threadHasHistory(thread)) {
    return `<a class="version-badge" href="#history/${encodeURIComponent(thread.id)}/${version}" target="_blank" rel="noopener" title="このバージョンの編集履歴を別タブで開く">v${version}</a>`;
  }
  return `<span class="version-badge">v${version}</span>`;
}

function renderComments(thread) {
  const comments = commentsForThread(thread.id);
  if (!comments.length) return `<div class="empty">まだコメントはありません。<br>下のフォームから最初のコメントをどうぞ。</div>`;
  return comments.map((comment) => {
    const isOwn = isOwnItem(comment);
    const label = authorLabel(comment);
    return `
    <article class="comment" data-author-color="${authorColorKey(comment.author)}" data-own="${isOwn}">
      <div class="comment-meta">
        <span>#${comment.number} <span class="comment-author" data-author-color="${authorColorKey(comment.author)}">${escapeHtml(label)}</span> ・ ${timeLabel(comment.createdAt)} ${versionBadge(thread, comment.threadVersion)}</span>
        ${canDeleteItem(comment) ? `<button class="comment-delete" data-action="delete-comment" data-comment-id="${escapeHtml(comment.id)}" title="このコメントを削除">削除</button>` : ""}
      </div>
      <div class="comment-body">${renderAnchoredText(comment.body)}</div>
      ${(comment.attachments || []).map(renderAttachmentPreview).join("")}
    </article>
  `;
  }).join("");
}

function renderThreadView() {
  const thread = state.threads.find((item) => item.id === state.selectedThreadId);
  if (!thread) {
    return `<div class="panel" style="padding:18px;">スレが見つかりません。</div>`;
  }
  const count = commentsForThread(thread.id).length;
  const isOwn = isOwnItem(thread);
  return `
    <section class="panel thread-shell ${state.commentsCollapsed ? "comments-collapsed" : ""}">
      <div class="thread-main">
        <div class="thread-header">
          <div>
            <h1>${escapeHtml(thread.title)} ${versionBadge(thread, thread.currentVersion)}</h1>
            <div class="muted"><span class="type-badge" data-type="${escapeHtml(thread.type)}">${escapeHtml(typeIcon(thread.type))}</span> ${escapeHtml(authorLabel(thread))} ・ ${timeLabel(thread.createdAt)}</div>
          </div>
          <div class="top-actions">
            ${isOwn ? `<button data-action="edit-thread">編集</button>` : ""}
            ${threadHasHistory(thread) ? `<button data-action="open-history">履歴</button>` : ""}
            ${canDeleteItem(thread) ? `<button class="danger-ghost" data-action="delete-thread">削除</button>` : ""}
            <button data-action="home">← 一覧へ</button>
          </div>
        </div>
        ${renderThreadBody(thread)}
      </div>
      ${state.commentsCollapsed ? `
        <aside class="comment-pane collapsed">
          <button class="comment-tab" data-action="toggle-comments">コメント ${count}</button>
        </aside>
      ` : `
        <aside class="comment-pane">
          <div class="comment-head">
            <strong>コメント <span class="count-pill">${count}</span></strong>
            <button data-action="toggle-comments">たたむ →</button>
          </div>
          <div class="comment-list">
            ${renderComments(thread)}
          </div>
          ${renderCommentForm()}
        </aside>
      `}
    </section>
  `;
}

function renderHistoryView() {
  const history = state.history;
  if (!history) return `<div class="panel" style="padding:18px;">履歴が見つかりません。</div>`;
  const thread = state.threads.find((item) => item.id === history.threadId);
  const title = thread ? thread.title : (history.versions.find((version) => version.isCurrent)?.title || "");
  return `
    <section class="section-head">
      <div>
        <h1>編集履歴: ${escapeHtml(title)}</h1>
        <div class="muted">世代を選ぶと、その編集で変わった内容を差分表示します。</div>
      </div>
      <div class="history-head-actions">
        <div class="tabs" role="tablist">
          <button class="tab" role="tab" aria-selected="${history.viewMode !== "split"}" data-action="history-view-mode" data-mode="unified">unified</button>
          <button class="tab" role="tab" aria-selected="${history.viewMode === "split"}" data-action="history-view-mode" data-mode="split">split</button>
        </div>
        <button data-action="open-thread" data-thread-id="${escapeHtml(history.threadId)}">← スレへ戻る</button>
      </div>
    </section>
    <section class="panel history-shell">
      <div class="history-list">
        ${renderHistoryVersions(history)}
      </div>
      <div class="history-diff">
        ${renderHistoryDiff(history)}
      </div>
    </section>
  `;
}

function renderHistoryVersions(history) {
  if (history.loading) return `<div class="empty">読み込み中…</div>`;
  if (!history.versions.length) return `<div class="empty">編集履歴を取得できませんでした。</div>`;
  if (history.versions.length === 1) return `<div class="empty">記録された編集履歴はありません。</div>`;
  return history.versions.map((version) => `
    <button class="history-version" aria-selected="${version.seq === history.selectedSeq}" data-action="select-history-version" data-seq="${version.seq}">
      <span class="history-version-head">v${Number(version.seq)}
        ${version.isCurrent ? `<span class="history-badge">現在</span>` : ""}
        ${version.seq === 1 ? `<span class="history-badge">初版</span>` : ""}
      </span>
      <span class="history-version-meta">
        <span class="comment-author" data-author-color="${authorColorKey(version.authorName)}">${escapeHtml(version.authorName)} ${escapeHtml(displayDeviceMark(version.authorDeviceId))}</span>
        ・ ${timeLabel(version.createdAt)}
      </span>
    </button>
  `).join("");
}

function renderHistoryDiff(history) {
  if (history.error) {
    return `
      <div class="error">${escapeHtml(history.error)}</div>
      <button data-action="retry-history">再試行</button>
    `;
  }
  if (history.loading || history.diffLoading) return `<div class="empty">読み込み中…</div>`;
  if (history.versions.length <= 1) return `<div class="empty">このスレにはまだ表示できる差分がありません。</div>`;
  const diff = history.diff;
  if (!diff) return `<div class="empty">左の一覧から世代を選択してください。</div>`;
  const titleLine = diff.titleChanged
    ? `<div class="history-title-change">タイトル: <del>${escapeHtml(diff.oldTitle)}</del> → <ins>${escapeHtml(diff.newTitle)}</ins></div>`
    : "";
  if (diff.tooLarge) {
    return `
      ${titleLine}
      <div class="empty">差分が大きすぎるため全文を表示します。</div>
      ${renderHistoryFallback(history, diff)}
    `;
  }
  if (!diff.hunks?.length) {
    return `${titleLine}<div class="empty">本文の変更はありません。</div>`;
  }
  const renderHunk = history.viewMode === "split" ? renderDiffHunkSplit : renderDiffHunk;
  return `${titleLine}${renderHistoryHunks(diff.hunks, renderHunk)}`;
}

function renderHistoryHunks(hunks, renderHunk = renderDiffHunk) {
  const parts = [];
  let prevOldEnd = null;
  for (const hunk of hunks) {
    if (prevOldEnd === null) {
      if (hunk.oldStart > 1) parts.push(`<div class="diff-separator">… ${hunk.oldStart - 1}行省略</div>`);
    } else {
      parts.push(`<div class="diff-separator">… ${hunk.oldStart - prevOldEnd}行省略</div>`);
    }
    parts.push(renderHunk(hunk));
    prevOldEnd = hunk.oldStart + hunk.lines.filter((line) => line.op !== "add").length;
  }
  return parts.join("");
}

function renderDiffHunk(hunk) {
  let oldNo = hunk.oldStart;
  let newNo = hunk.newStart;
  const rows = hunk.lines.map((line) => {
    const sign = line.op === "add" ? "+" : line.op === "del" ? "−" : " ";
    const oldLabel = line.op === "add" ? "" : String(oldNo++);
    const newLabel = line.op === "del" ? "" : String(newNo++);
    return `<div class="diff-line" data-op="${escapeHtml(line.op)}"><span class="diff-line-num">${oldLabel}</span><span class="diff-line-num">${newLabel}</span><span class="diff-line-sign">${sign}</span><span class="diff-line-text">${escapeHtml(line.text) || " "}</span></div>`;
  }).join("");
  return `<div class="diff-hunk">${rows}</div>`;
}

// hunk の行を split 表示用の行ペア(left=旧版, right=新版)に変換する。
// ctx は左右同一行、ctx に挟まれた del/add の連続ブロックは del 群と add 群を
// i 番目同士で横に並べ、余った側は null(空セル)にする(GitHub と同じ)。
function buildSplitRows(lines) {
  const rows = [];
  let i = 0;
  while (i < lines.length) {
    if (lines[i].op === "ctx") {
      rows.push({ left: lines[i], right: lines[i] });
      i++;
      continue;
    }
    const dels = [];
    const adds = [];
    while (i < lines.length && lines[i].op !== "ctx") {
      if (lines[i].op === "del") dels.push(lines[i]);
      else adds.push(lines[i]);
      i++;
    }
    const count = Math.max(dels.length, adds.length);
    for (let j = 0; j < count; j++) {
      rows.push({ left: dels[j] || null, right: adds[j] || null });
    }
  }
  return rows;
}

function renderDiffHunkSplit(hunk) {
  let oldNo = hunk.oldStart;
  let newNo = hunk.newStart;
  const rows = buildSplitRows(hunk.lines).map((row) => {
    const left = row.left
      ? `<span class="diff-line-num">${oldNo++}</span><span class="diff-line-text">${escapeHtml(row.left.text) || " "}</span>`
      : `<span class="diff-line-num"></span><span class="diff-line-text"></span>`;
    const right = row.right
      ? `<span class="diff-line-num">${newNo++}</span><span class="diff-line-text">${escapeHtml(row.right.text) || " "}</span>`
      : `<span class="diff-line-num"></span><span class="diff-line-text"></span>`;
    const leftOp = row.left ? row.left.op : "empty";
    const rightOp = row.right ? row.right.op : "empty";
    return `<div class="diff-split-row"><span class="diff-split-cell" data-op="${escapeHtml(leftOp)}">${left}</span><span class="diff-split-cell" data-op="${escapeHtml(rightOp)}">${right}</span></div>`;
  }).join("");
  return `<div class="diff-hunk">${rows}</div>`;
}

function renderHistoryFallback(history, diff) {
  const showNew = history.fallbackSide !== "old";
  const text = showNew ? (diff.newBody || "") : (diff.oldBody || "");
  return `
    <div class="tabs" role="tablist">
      <button class="tab" role="tab" aria-selected="${!showNew}" data-action="history-fallback-side" data-side="old">旧版</button>
      <button class="tab" role="tab" aria-selected="${showNew}" data-action="history-fallback-side" data-side="new">新版</button>
    </div>
    <div class="content-view text-body">${escapeHtml(text)}</div>
  `;
}

function renderCommentForm() {
  const draft = ensureCommentDraft();
  return `
    <form class="comment-form" data-form="comment">
      <textarea class="comment-input" data-comment-body placeholder="コメントを書く。>>1 でアンカー。Ctrl+V / drop で添付。">${escapeHtml(draft.body)}</textarea>
      <div class="chips">
        ${draft.attachments.map((attachment) => `
          <span class="chip">
            ${escapeHtml(attachment.name)}
            <button type="button" data-action="remove-draft-attachment" data-attachment-id="${escapeHtml(attachment.id)}">x</button>
          </span>
        `).join("")}
      </div>
      <div style="display:flex; justify-content:flex-end; margin-top:8px;">
        <button class="primary" type="submit">投稿する</button>
      </div>
    </form>
  `;
}

app.addEventListener("click", (event) => {
  const target = event.target.closest("[data-action]");
  if (!target) return;

  const action = target.dataset.action;
  if (action === "home") goHome();
  if (action === "create") goCreate();
  if (action === "edit-author") editAuthor();
  if (action === "open-thread") goThread(target.dataset.threadId);
  if (action === "set-create-type") setCreateType(target.dataset.type);
  if (action === "create-thread") createThreadFromDraft();
  if (action === "remove-draft-attachment") removeDraftAttachment(target.dataset.attachmentId);
  if (action === "edit-thread") startEditThread();
  if (action === "delete-thread") deleteThread();
  if (action === "delete-comment") deleteComment(target.dataset.commentId);
  if (action === "save-thread-edit") saveThreadEdit();
  if (action === "open-history") goHistory(state.selectedThreadId);
  if (action === "select-history-version") selectHistoryVersion(Number(target.dataset.seq));
  if (action === "retry-history") goHistory(state.history?.threadId || state.selectedThreadId);
  if (action === "history-view-mode") setHistoryViewMode(target.dataset.mode);
  if (action === "history-fallback-side" && state.history) {
    state.history.fallbackSide = target.dataset.side;
    renderApp();
  }
  if (action === "toggle-comments") {
    state.commentsCollapsed = !state.commentsCollapsed;
    renderApp();
  }
});

app.addEventListener("input", (event) => {
  if (event.target.matches("[data-edit-title]")) {
    state.drafts.edit.title = event.target.value;
    return;
  }
  if (event.target.matches("[data-edit-body]")) {
    state.drafts.edit.body = event.target.value;
    refreshEditPreview();
    return;
  }
  if (event.target.matches("[data-comment-body]")) {
    ensureCommentDraft().body = event.target.value;
    return;
  }
  const field = event.target.dataset.field;
  if (!field || state.view !== "create") return;
  updateDraftField(field, event.target.value);
  refreshCreatePreview();
});

app.addEventListener("submit", (event) => {
  if (event.target.dataset.form === "comment") {
    event.preventDefault();
    postComment();
  }
});

app.addEventListener("change", (event) => {
  if (state.view !== "create") return;
  if (event.target.dataset.action === "choose-create-file") {
    handleCreateFiles(event.target.files);
  }
});

app.addEventListener("paste", (event) => {
  if (!event.target.matches("[data-comment-body]")) return;
  const files = Array.from(event.clipboardData?.files || []);
  if (files.length) {
    event.preventDefault();
    addCommentAttachments(files);
  }
});

app.addEventListener("dragover", (event) => {
  const commentInput = event.target.closest("[data-comment-body]");
  if (commentInput) {
    event.preventDefault();
    return;
  }
  const zone = event.target.closest("[data-drop]");
  if (!zone) return;
  event.preventDefault();
  zone.classList.add("dragover");
});

app.addEventListener("dragleave", (event) => {
  const zone = event.target.closest("[data-drop]");
  if (zone) zone.classList.remove("dragover");
});

app.addEventListener("drop", (event) => {
  const commentInput = event.target.closest("[data-comment-body]");
  if (commentInput) {
    event.preventDefault();
    addCommentAttachments(event.dataTransfer.files);
    return;
  }
  const zone = event.target.closest("[data-drop]");
  if (!zone) return;
  event.preventDefault();
  zone.classList.remove("dragover");
  if (zone.dataset.drop === "create") {
    handleCreateFiles(event.dataTransfer.files);
  }
});

window.addEventListener("message", (event) => {
  const data = event.data;
  if (!data || data.type !== "revu-frame-height") return;
  // frameIdだけでなく送信元windowも照合して、他フレームからの偽装を防ぐ
  const frames = app.querySelectorAll("iframe.html-frame[data-frame-id]");
  for (const frame of frames) {
    if (frame.contentWindow !== event.source) continue;
    if (frame.dataset.frameId !== String(data.frameId)) continue;
    const height = Math.max(120, Math.min(Math.ceil(Number(data.height) || 0), 20000));
    frame.style.height = `${height}px`;
    break;
  }
});

// --- 選択テキストの「コメントに追加」ポップアップ (#3) ---
// body 直下のシングルトン要素。renderApp() の全体再描画に巻き込まれない。
let quotePopupEl = null;
let pendingQuoteText = "";

function ensureQuotePopup() {
  if (quotePopupEl) return quotePopupEl;
  quotePopupEl = document.createElement("div");
  quotePopupEl.className = "quote-popup";
  quotePopupEl.hidden = true;
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = "コメントに追加";
  button.addEventListener("click", () => {
    const text = pendingQuoteText;
    hideQuotePopup();
    window.getSelection()?.removeAllRanges();
    appendQuoteToCommentDraft(text);
  });
  quotePopupEl.appendChild(button);
  document.body.appendChild(quotePopupEl);
  return quotePopupEl;
}

function hideQuotePopup() {
  if (quotePopupEl) quotePopupEl.hidden = true;
  pendingQuoteText = "";
}

function showQuotePopup(selection) {
  // ボタンクリック時には選択が解除されている場合があるため、表示時点の文字列を保持する
  pendingQuoteText = selection.toString();
  const rect = selection.getRangeAt(selection.rangeCount - 1).getBoundingClientRect();
  const el = ensureQuotePopup();
  el.hidden = false;
  const width = el.offsetWidth;
  const height = el.offsetHeight;
  const maxLeft = window.scrollX + document.documentElement.clientWidth - width - 8;
  const left = Math.max(8, Math.min(rect.left + rect.width / 2 - width / 2 + window.scrollX, maxLeft));
  // 画面下端で見切れる場合は選択範囲の上側に表示する
  const below = rect.bottom + 6 + height <= document.documentElement.clientHeight;
  const top = below ? rect.bottom + 6 : Math.max(8, rect.top - height - 6);
  el.style.left = `${left}px`;
  el.style.top = `${top + window.scrollY}px`;
}

function quotableSelection() {
  if (state.view !== "thread") return null;
  const thread = state.threads.find((item) => item.id === state.selectedThreadId);
  if (!thread || (thread.type !== "markdown" && thread.type !== "text")) return null;
  const selection = window.getSelection();
  if (!selection || selection.isCollapsed || !selection.toString().trim()) return null;
  const preview = app.querySelector(".thread-main .content-view");
  if (!preview) return null;
  const within = (node) =>
    node && preview.contains(node.nodeType === Node.TEXT_NODE ? node.parentNode : node);
  // 始点・終点の両方がプレビュー本文内にある選択だけを対象にする
  return within(selection.anchorNode) && within(selection.focusNode) ? selection : null;
}

document.addEventListener("mouseup", (event) => {
  if (quotePopupEl && quotePopupEl.contains(event.target)) return;
  // mouseup 直後は selection が確定していないことがあるため1tick遅らせる
  setTimeout(() => {
    const selection = quotableSelection();
    if (selection) {
      showQuotePopup(selection);
    } else {
      hideQuotePopup();
    }
  }, 0);
});

document.addEventListener("mousedown", (event) => {
  if (quotePopupEl && !quotePopupEl.contains(event.target)) hideQuotePopup();
});

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") hideQuotePopup();
});

document.addEventListener("scroll", () => hideQuotePopup(), true);

function consumeHistoryDeepLink() {
  const match = /^#history\/([^/]+)\/(\d+)$/.exec(location.hash || "");
  if (!match) return null;
  let threadId;
  try {
    threadId = decodeURIComponent(match[1]);
  } catch {
    return null;
  }
  // ハッシュは起動パラメータとして一度だけ使う。本格ルーティングは
  // 導入しないため、読んだらURLから消して以後のズレを防ぐ
  history.replaceState(null, "", location.pathname + location.search);
  return { threadId, seq: Number(match[2]) };
}

function boot() {
  if (window.mermaid) {
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      theme: "neutral",
      // Mermaid SVGは strict モードで生成し、そのまま差し込む。
      // 追加の DOMPurify は Mermaid のノードラベルを消すことがある。
      flowchart: { htmlLabels: false, useMaxWidth: false },
      class: { useMaxWidth: false },
      sequence: { useMaxWidth: false },
      er: { useMaxWidth: false },
      state: { useMaxWidth: false },
      journey: { useMaxWidth: false },
      gantt: { useMaxWidth: false }
    });
  }
  const deepLink = consumeHistoryDeepLink();
  loadState()
    .then(() => {
      if (!deepLink) return;
      if (state.threads.some((thread) => thread.id === deepLink.threadId)) {
        return goHistory(deepLink.threadId, deepLink.seq);
      }
      pushError("指定されたスレが見つかりませんでした。");
    })
    .catch((error) => {
      pushError(apiErrorMessage(error, "スレッド一覧を読み込めませんでした。"));
    })
    .finally(renderApp);
}

boot();
