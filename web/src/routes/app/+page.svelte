<script lang="ts">
	import { onMount, tick } from 'svelte';
	import Button from '#lib/components/Button.svelte';
	import Field from '#lib/components/Field.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import MailBody from '#lib/components/MailBody.svelte';
	import Modal from '#lib/components/Modal.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import { mailApi } from '#lib/mail-api';
	import { addressList, bytes, dateTime, relativeTime } from '#lib/format';
	import { userSession } from '#lib/user-session.svelte';
	import { toast } from '#lib/toast.svelte';
	import { iconButton, textarea } from '#lib/ui';
	import type { IconName } from '#lib/icons';
	import type {
		Conversation,
		MailFolder,
		MailMessage,
		MailSearchHit,
		ReplyTarget
	} from '#lib/mail-types';

	const ORDER = [2, 3, 6, 5, 4, 12];

	let folders = $state<MailFolder[]>([]);
	let folderId = $state<number | null>(null);
	let conversations = $state<Conversation[]>([]);
	let messages = $state<MailMessage[]>([]);
	let selected = $state<Conversation | null>(null);
	let loading = $state(true);
	let loadingThread = $state(false);
	let refreshing = $state(false);
	let messagesEl = $state<HTMLDivElement | null>(null);
	let sentFolderId = $state(0);

	// A reply and a new message are the same editor: one draft, two defaults.
	type DraftMode = 'reply' | 'compose';

	type Draft = {
		mode: DraftMode;
		// Message being replied to; 0 for a new message.
		messageId: number;
		to: string;
		cc: string;
		bcc: string;
		subject: string;
		body: string;
		busy: boolean;
		// Recipients the dialog can restore.
		baseTo: string;
		baseCc: string;
	};

	let draft = $state<Draft | null>(null);
	// Conversation to return to when a new message is abandoned.
	let composeReturn = $state<{ conversation: Conversation; folder: number } | null>(null);
	let composeDraftRestored = $state(false);
	// The draft kept in the browser, so the list can show it while not editing.
	let savedDraft = $state<{ to: string; cc: string; bcc: string; subject: string; body: string } | null>(
		null
	);

	let recipientsOpen = $state(false);
	let draftTo = $state('');
	let draftCc = $state('');
	let draftBcc = $state('');

	// The editor grows with its text, up to 40% of the viewport height.
	let draftBodyEl = $state<HTMLTextAreaElement | null>(null);

	function fitDraftBody() {
		const el = draftBodyEl;
		if (!el) return;
		const max = Math.round(window.innerHeight * 0.4);
		el.style.height = 'auto';
		const wanted = el.scrollHeight;
		el.style.height = `${Math.min(wanted, max)}px`;
		el.style.overflowY = wanted > max ? 'auto' : 'hidden';
	}

	$effect(() => {
		// Re-fit on mode switches, restored drafts and every edit.
		void draftBodyEl;
		void draft?.mode;
		void draft?.body;
		queueMicrotask(fitDraftBody);
	});

	let searchQuery = $state('');
	let searchHits = $state<MailSearchHit[]>([]);
	let searchTotal = $state(0);
	let searching = $state(false);
	let searchOpen = $state(false);
	let searchInput = $state<HTMLInputElement | null>(null);
	// Last submitted term, so the list can tell "not searched yet" from "no match".
	let searchSubmitted = $state('');
	// Folder of the conversation currently open; a search hit may live in a
	// folder that is not the selected one.
	let openFolderId = $state<number | null>(null);

	let searching$active = $derived(searchQuery.trim() !== '');
	let searchPending = $derived(searchSubmitted !== searchQuery.trim());
	let draftToList = $derived(draft ? splitAddresses(draft.to) : []);
	let draftCcList = $derived(draft ? splitAddresses(draft.cc) : []);
	let draftBccList = $derived(draft ? splitAddresses(draft.bcc) : []);
	let draftRecipientCount = $derived(
		draftToList.length + draftCcList.length + draftBccList.length
	);
	let draftRecipientSummary = $derived(
		draftRecipientCount === 0
			? '选择收件人'
			: draftRecipientCount === 1
				? (draftToList[0] ?? draftCcList[0] ?? '')
				: `${draftToList[0] ?? draftCcList[0] ?? ''} 等 ${draftRecipientCount} 人`
	);
	let draftCanSend = $derived(Boolean(draft && draftToList.length > 0 && draft.body.trim() !== ''));
	let composing = $derived(draft?.mode === 'compose');

	let currentFolder = $derived(folders.find((f) => f.id === folderId) ?? null);

	// A thread is a conversation: my own messages sit on the right, everyone
	// else on the left. Sent messages are identified by folder first (the
	// account may send from an alias), with the primary address as a fallback.
	let myAddress = $derived((userSession.profile?.address ?? '').toLowerCase());

	function isOutgoing(message: MailMessage): boolean {
		return (
			message.folderId === sentFolderId ||
			(myAddress !== '' && message.fromAddr.trim().toLowerCase() === myAddress)
		);
	}

	// Everyone except me who wrote in this thread.
	let otherSenders = $derived.by(() => {
		const byAddress = new Map<string, { name: string; address: string }>();
		for (const message of messages) {
			if (isOutgoing(message)) continue;
			const address = message.fromAddr.trim();
			const key = address.toLowerCase();
			if (key === '' || byAddress.has(key)) continue;
			byAddress.set(key, { name: message.fromName.trim(), address });
		}
		return [...byAddress.values()];
	});
	// One counterpart: their identity lives in the thread header only. A second
	// sender makes it ambiguous, so each message carries its own address.
	let counterpart = $derived(otherSenders.length === 1 ? otherSenders[0] : null);
	let multiSender = $derived(otherSenders.length > 1);

	function rank(folder: MailFolder): number {
		const index = ORDER.indexOf(folder.type);
		return index < 0 ? ORDER.length + folder.id / 1e6 : index;
	}

	function folderIcon(type: number): IconName {
		switch (type) {
			case 2:
				return 'mail';
			case 5:
				return 'arrowRight';
			case 3:
				return 'edit';
			case 4:
				return 'trash';
			case 12:
				return 'alert';
			default:
				return 'mail';
		}
	}

	function listTime(unix: number): string {
		const date = new Date(unix * 1000);
		const now = new Date();
		if (date.toDateString() === now.toDateString()) {
			return date.toLocaleTimeString('zh-CN', {
				hour: '2-digit',
				minute: '2-digit',
				hour12: false
			});
		}
		if (date.getFullYear() === now.getFullYear()) {
			return `${date.getMonth() + 1}月${date.getDate()}日`;
		}
		return `${date.getFullYear()}/${date.getMonth() + 1}/${date.getDate()}`;
	}

	/** Compact timestamp for a conversation bubble. */
	function messageTime(unix: number): string {
		const date = new Date(unix * 1000);
		const now = new Date();
		const time = date.toLocaleTimeString('zh-CN', {
			hour: '2-digit',
			minute: '2-digit',
			hour12: false
		});
		if (date.toDateString() === now.toDateString()) return time;
		if (date.getFullYear() === now.getFullYear()) {
			return `${date.getMonth() + 1}月${date.getDate()}日 ${time}`;
		}
		return `${date.getFullYear()}/${date.getMonth() + 1}/${date.getDate()} ${time}`;
	}

	/** Hover text carrying the full header of one message. */
	function messageDetails(message: MailMessage): string {
		const lines = [
			`发件人：${message.fromName ? `${message.fromName} <${message.fromAddr}>` : message.fromAddr}`
		];
		if (message.to) lines.push(`收件人：${addressList(message.to)}`);
		if (message.cc) lines.push(`抄送：${addressList(message.cc)}`);
		lines.push(`时间：${dateTime(message.date)}`);
		if (message.folderId === sentFolderId) lines.push('状态：已发送');
		return lines.join('\n');
	}

	/** Sender as "昵称 <邮箱>", or just the address when there is no nickname. */
	function senderLabel(message: MailMessage): string {
		const name = message.fromName.trim();
		const address = message.fromAddr.trim();
		if (name === '') return address;
		return address === '' ? name : `${name} <${address}>`;
	}

	async function reloadFolders() {
		const res = await mailApi.folders();
		folders = [...res.folders].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
		folderId ??= folders.find((f) => f.type === 2)?.id ?? folders[0]?.id ?? null;
	}

	async function loadConversations() {
		if (!folderId) return;
		const res = await mailApi.conversations(folderId);
		conversations = res.conversations;
	}

	onMount(async () => {
		const stored = readComposeDraft();
		if (stored.to || stored.cc || stored.bcc || stored.subject || stored.body) {
			savedDraft = {
				to: stored.to ?? '',
				cc: stored.cc ?? '',
				bcc: stored.bcc ?? '',
				subject: stored.subject ?? '',
				body: stored.body ?? ''
			};
		}
		try {
			await reloadFolders();
			await loadConversations();
		} catch (err) {
			toast.fail(err, '无法加载邮箱');
		} finally {
			loading = false;
		}
	});

	async function selectFolder(id: number) {
		// Leaving the editor happens even when the folder is already selected, so
		// clicking the current folder returns to its conversation list.
		leaveDraft();
		if (id === folderId) return;
		folderId = id;
		selected = null;
		messages = [];
		refreshing = true;
		try {
			await loadConversations();
		} catch (err) {
			toast.fail(err, '无法读取文件夹');
		} finally {
			refreshing = false;
		}
	}

	async function refresh() {
		if (!folderId) return;
		refreshing = true;
		try {
			await loadConversations();
			await reloadFolders();
		} catch (err) {
			toast.fail(err, '刷新失败');
		} finally {
			refreshing = false;
		}
	}

	async function open(conversation: Conversation, folder = folderId) {
		if (!folder) return;
		// Opening a thread replaces the editor; a new message keeps its draft in
		// the browser for the next 写邮件 click.
		leaveDraft();
		selected = conversation;
		openFolderId = folder;
		messages = [];
		loadingThread = true;
		try {
			const res = await mailApi.thread(folder, conversation.key);
			messages = res.messages;
			sentFolderId = res.sentFolderId;
			if (res.replyTarget) startReply(res.replyTarget);
			else draft = null;
			selected = { ...conversation, count: res.messages.length || conversation.count };
			const unread = messages.filter((m) => !m.isRead);
			if (unread.length > 0) {
				await Promise.all(unread.map((m) => mailApi.markRead(m.id)));
				messages = messages.map((m) => ({ ...m, isRead: true }));
				conversations = conversations.map((item) =>
					item.key === conversation.key ? { ...item, unread: 0 } : item
				);
				selected = { ...(selected ?? conversation), unread: 0 };
				await reloadFolders();
			}
		} catch (err) {
			toast.fail(err, '无法打开会话');
			selected = null;
		} finally {
			loadingThread = false;
		}
		await tick();
		if (messagesEl) messagesEl.scrollTop = messagesEl.scrollHeight;
	}

	function closeReader() {
		draft = null;
		selected = null;
		messages = [];
		openFolderId = null;
	}

	// Searches only run on submit: the server is small and a per-keystroke
	// request would be far too heavy.
	async function submitSearch(event: SubmitEvent) {
		event.preventDefault();
		const query = searchQuery.trim();
		if (query === '' || searching) return;
		searching = true;
		try {
			const res = await mailApi.search(query);
			searchHits = res.hits;
			searchTotal = res.total;
			searchSubmitted = query;
		} catch (err) {
			toast.fail(err, '搜索失败');
		} finally {
			searching = false;
		}
	}

	function onSearchInput() {
		// Editing invalidates the previous result set, and typing never triggers
		// a request.
		if (searchQuery.trim() === searchSubmitted) return;
		searchHits = [];
		searchTotal = 0;
	}

	function clearSearch() {
		searchQuery = '';
		searchHits = [];
		searchTotal = 0;
		searching = false;
		searchSubmitted = '';
	}

	async function toggleSearch() {
		if (searchOpen) {
			searchOpen = false;
			clearSearch();
			return;
		}
		searchOpen = true;
		await tick();
		searchInput?.focus();
	}

	async function openHit(hit: MailSearchHit) {
		const conversation: Conversation = {
			key: hit.key,
			subject: hit.subject,
			from: { name: hit.fromName, address: hit.fromAddr },
			to: hit.to,
			lastAt: hit.date,
			count: 1,
			unread: hit.isRead ? 0 : 1,
			hasAttachments: false,
			preview: hit.preview,
			latestMessageId: hit.id
		};
		await open(conversation, hit.folderId);
	}

	/** Accepts "a@x, b@y" and returns trimmed entries. */
	function splitAddresses(value: string): string[] {
		return value
			.split(/[;,，、\s]+/)
			.map((item) => item.trim())
			.filter((item) => item !== '');
	}

	function joinAddresses(list: string[]): string {
		return list.join(', ');
	}

	/** Builds the reply draft from the thread's default target. */
	function startReply(target: ReplyTarget) {
		draft = {
			mode: 'reply',
			messageId: target.messageId,
			to: joinAddresses(target.to ?? []),
			cc: joinAddresses(target.cc ?? []),
			bcc: '',
			subject: target.subject,
			body: '',
			busy: false,
			baseTo: joinAddresses(target.to ?? []),
			baseCc: joinAddresses(target.cc ?? [])
		};
	}

	/** Drops the editor; a new message keeps its draft in the browser. */
	function leaveDraft() {
		if (!draft) return;
		if (draft.mode === 'compose') saveComposeDraft();
		else clearTimeout(composeTimer);
		draft = null;
		composeReturn = null;
	}

	function openCompose() {
		const restored = savedDraft ?? readComposeDraft();
		composeDraftRestored = Boolean(
			restored.to || restored.cc || restored.bcc || restored.subject || restored.body
		);
		composeReturn =
			selected && folderId ? { conversation: selected, folder: openFolderId ?? folderId } : null;
		selected = null;
		messages = [];
		draft = {
			mode: 'compose',
			messageId: 0,
			to: restored.to ?? '',
			cc: restored.cc ?? '',
			bcc: restored.bcc ?? '',
			subject: restored.subject ?? '',
			body: restored.body ?? '',
			busy: false,
			baseTo: '',
			baseCc: ''
		};
	}

	/** Leaves the editor and, for a new message, returns to the previous thread. */
	async function closeDraft(reopenReply = true) {
		const back = composeReturn;
		const replyKey = selected?.key;
		const replyFolder = openFolderId ?? folderId;
		leaveDraft();
		if (back) {
			await open(back.conversation, back.folder);
			return;
		}
		if (reopenReply && replyKey) {
			const updated = conversations.find((item) => item.key === replyKey);
			if (updated) await open(updated, replyFolder);
		}
	}

	const COMPOSE_DRAFT_KEY = 'lifemail.compose.draft';
	let composeTimer: ReturnType<typeof setTimeout> | undefined;

	function readComposeDraft(): Partial<Draft> {
		try {
			const raw = localStorage.getItem(COMPOSE_DRAFT_KEY);
			return raw ? (JSON.parse(raw) as Partial<Draft>) : {};
		} catch {
			localStorage.removeItem(COMPOSE_DRAFT_KEY);
			return {};
		}
	}

	function saveComposeDraft() {
		clearTimeout(composeTimer);
		if (draft?.mode !== 'compose') return;
		const saved = {
			to: draft.to,
			cc: draft.cc,
			bcc: draft.bcc,
			subject: draft.subject,
			body: draft.body
		};
		if (!saved.to && !saved.cc && !saved.bcc && !saved.subject && !saved.body) {
			localStorage.removeItem(COMPOSE_DRAFT_KEY);
			savedDraft = null;
			return;
		}
		localStorage.setItem(COMPOSE_DRAFT_KEY, JSON.stringify(saved));
		savedDraft = saved;
	}

	/** Keeps the draft in the browser only: there is no autosave endpoint. */
	function scheduleComposeSave() {
		if (draft?.mode !== 'compose') return;
		clearTimeout(composeTimer);
		composeTimer = setTimeout(saveComposeDraft, 800);
	}

	function discardComposeDraft() {
		clearTimeout(composeTimer);
		localStorage.removeItem(COMPOSE_DRAFT_KEY);
		savedDraft = null;
	}

	function openRecipients() {
		if (!draft) return;
		draftTo = draft.to;
		draftCc = draft.cc;
		draftBcc = draft.bcc;
		recipientsOpen = true;
	}

	function applyRecipients(event: SubmitEvent) {
		event.preventDefault();
		if (!draft) {
			recipientsOpen = false;
			return;
		}
		draft.to = joinAddresses(splitAddresses(draftTo));
		draft.cc = joinAddresses(splitAddresses(draftCc));
		draft.bcc = joinAddresses(splitAddresses(draftBcc));
		if (draft.mode === 'compose') scheduleComposeSave();
		recipientsOpen = false;
	}

	/** Restores the reply defaults inside the recipient dialog. */
	function useDraftRecipients(all: boolean) {
		if (!draft) return;
		draftTo = all ? draft.baseTo : (splitAddresses(draft.baseTo)[0] ?? '');
		draftCc = all ? draft.baseCc : '';
	}

	async function sendDraft() {
		const current = draft;
		if (!current || current.busy || !draftCanSend) return;
		const to = draftToList;
		const cc = draftCcList;
		const bcc = draftBccList;
		current.busy = true;
		try {
			if (current.mode === 'reply') {
				await mailApi.reply({
					messageId: current.messageId,
					subject: current.subject,
					body: current.body.trim(),
					to,
					cc
				});
			} else {
				await mailApi.send({
					to,
					cc,
					bcc,
					subject: current.subject.trim(),
					body: current.body
				});
			}
			const wasReply = current.mode === 'reply';
			if (!wasReply) discardComposeDraft();
			draft = null;
			toast.ok(wasReply ? '回复已发送' : '邮件已发送');
			await refresh();
			await closeDraft();
		} catch (err) {
			toast.fail(err, '发送失败');
			if (draft) draft.busy = false;
		}
	}

	function onDraftKeydown(event: KeyboardEvent) {
		// Ctrl/⌘+Enter sends; a plain Enter breaks the line, so a half-written
		// mail is never sent by accident (there is no unsend here).
		if (event.key === 'Enter' && (event.ctrlKey || event.metaKey) && !event.isComposing) {
			event.preventDefault();
			void sendDraft();
		}
	}
</script>

<svelte:head>
	<title>邮件 · lifemail</title>
</svelte:head>

{#snippet messageBody(message: MailMessage, corner: string)}
	<article
		class="w-full min-w-0 rounded-2xl border border-zinc-200 bg-white px-4 py-3 shadow-xs dark:border-zinc-800 dark:bg-zinc-900/60 {corner}"
	>
		<MailBody html={message.bodyHtml} text={message.bodyText} />

		{#if message.attachments.length > 0}
			<div class="mt-4 border-t border-zinc-100 pt-3 dark:border-zinc-800">
				<p class="muted mb-2 text-[11px] font-medium">
					附件 · {message.attachments.length} 个
				</p>
				<ul class="flex flex-wrap gap-2">
					{#each message.attachments as attachment (attachment.id)}
						<li>
							<a
								href={mailApi.attachmentUrl(attachment.id)}
								class="inline-flex max-w-full items-center gap-2 rounded-lg border border-zinc-200 bg-zinc-50 px-2.5 py-1.5 text-[12px] transition-colors hover:border-brand-300 hover:bg-brand-50 dark:border-zinc-700 dark:bg-zinc-800 dark:hover:border-brand-500/50 dark:hover:bg-brand-500/10"
								download={attachment.fileName}
							>
								<Icon name="paperclip" size={13} />
								<span class="max-w-52 truncate font-medium"
									>{attachment.fileName || '附件'}</span
								>
								<span class="muted shrink-0 text-[10px]">{bytes(attachment.size)}</span>
							</a>
						</li>
					{/each}
				</ul>
			</div>
		{/if}
	</article>
{/snippet}

<!-- Mobile folder switcher -->
<div class="mb-3 flex gap-1 overflow-x-auto pb-1 [scrollbar-width:none] lg:hidden">
	{#each folders as folder (folder.id)}
		<button
			type="button"
			class="shrink-0 rounded-full px-3 py-1.5 text-xs font-medium transition-colors {folder.id ===
			folderId
				? 'bg-brand-600 text-white'
				: 'bg-white text-zinc-600 ring-1 ring-zinc-200 dark:bg-zinc-900 dark:text-zinc-300 dark:ring-zinc-800'}"
			onclick={() => selectFolder(folder.id)}
		>
			{folder.name}
			{#if folder.unread > 0}<span class="ml-1 opacity-80">{folder.unread}</span>{/if}
		</button>
	{/each}
</div>

<div class="lg:grid lg:h-[calc(100dvh-9rem)] lg:grid-cols-[11rem_20rem_minmax(0,1fr)] lg:gap-4">
	<!-- Folders -->
	<nav class="hidden min-h-0 overflow-y-auto lg:block">
		<ul class="space-y-0.5">
			{#each folders as folder (folder.id)}
				<li>
					<button
						type="button"
						class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13px] font-medium transition-colors {folder.id ===
						folderId
							? 'bg-brand-50 text-brand-700 dark:bg-brand-500/15 dark:text-brand-300'
							: 'text-zinc-600 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100'}"
						onclick={() => selectFolder(folder.id)}
					>
						<Icon name={folderIcon(folder.type)} size={15} />
						<span class="min-w-0 flex-1 truncate">{folder.name}</span>
						{#if folder.unread > 0}
							<span
								class="rounded-full bg-brand-600 px-1.5 py-0.5 text-[10px] font-semibold text-white"
							>
								{folder.unread}
							</span>
						{/if}
					</button>
				</li>
			{/each}
		</ul>
	</nav>

	<!-- Conversations -->
	<section
		class="surface flex min-h-[60vh] min-w-0 flex-col overflow-hidden lg:min-h-0 {selected ||
		draft
			? 'hidden lg:flex'
			: 'flex'}"
	>
		<header
			class="flex h-12 shrink-0 items-center gap-2 border-b border-zinc-200 px-3 dark:border-zinc-800"
		>
			<div class="min-w-0 flex-1">
				<p class="truncate text-[13px] font-semibold">
					{searching$active ? '搜索结果' : (currentFolder?.name ?? '邮件')}
				</p>
				<p class="muted text-[11px]"
					>{searching$active
						? searching
							? '搜索中…'
							: `${searchTotal} 封匹配`
						: `${conversations.length} 个会话`}</p
				>
			</div>
			<button
				type="button"
				class={iconButton}
				onclick={refresh}
				disabled={refreshing || loading}
				aria-label="刷新"
				title="刷新"
			>
				<Icon name="refresh" size={15} class={refreshing ? 'animate-spin' : ''} />
			</button>
			<button
				type="button"
				class="{iconButton} {searchOpen
					? 'bg-brand-50 text-brand-700 dark:bg-brand-500/15 dark:text-brand-300'
					: ''}"
				onclick={toggleSearch}
				aria-label={searchOpen ? '收起搜索' : '搜索'}
				title={searchOpen ? '收起搜索' : '搜索'}
			>
				<Icon name="search" size={15} />
			</button>
		</header>

		{#if searchOpen}
			<div class="shrink-0 border-b border-zinc-200 px-2.5 py-2 dark:border-zinc-800">
				<form class="flex items-center gap-1.5" onsubmit={submitSearch}>
					<div class="relative min-w-0 flex-1">
						<Icon
							name="search"
							size={13}
							class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-zinc-400"
						/>
						<input
							bind:this={searchInput}
							type="text"
							class="w-full rounded-lg border border-zinc-200 bg-white py-1.5 pr-7 pl-8 text-[12px] text-zinc-800 placeholder:text-zinc-400 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:focus:ring-brand-500/20"
							placeholder="搜索全部文件夹"
							bind:value={searchQuery}
							oninput={onSearchInput}
							aria-label="搜索邮件"
						/>
						{#if searchQuery}
							<button
								type="button"
								class="absolute top-1/2 right-1.5 -translate-y-1/2 rounded p-0.5 text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-200"
								onclick={clearSearch}
								aria-label="清空搜索"
							>
								<Icon name="x" size={12} />
							</button>
						{/if}
					</div>
					<Button type="submit" size="sm" disabled={searching || !searchQuery.trim()}>
						{searching ? '搜索中…' : '搜索'}
					</Button>
				</form>
			</div>
		{/if}

		<div class="min-h-0 flex-1 overflow-y-auto">
			{#if searching$active}
				{#if searching}
					<div class="p-3"><Skeleton rows={5} /></div>
				{:else if searchPending}
					<EmptyState
						icon="search"
						title="按回车或点「搜索」"
						description="为了不给服务器加压，输入时不会自动搜索。"
					/>
				{:else if searchHits.length === 0}
					<EmptyState
						icon="search"
						title="没有匹配的邮件"
						description="试试主题、发件人或正文里的其他关键词。"
					/>
				{:else}
					<ul class="divide-y divide-zinc-100 dark:divide-zinc-800">
						{#each searchHits as hit (hit.id)}
							<li>
								<button
									type="button"
									class="block w-full px-3 py-2.5 text-left transition-colors hover:bg-zinc-50 dark:hover:bg-zinc-800/60 {selected?.key ===
									hit.key
										? 'bg-zinc-100 dark:bg-zinc-800'
										: ''}"
									onclick={() => openHit(hit)}
								>
									<div class="flex items-baseline gap-2">
										<span
											class="min-w-0 flex-1 truncate text-[13px] {hit.isRead
												? 'font-medium'
												: 'font-semibold'}"
											>{hit.fromName || hit.fromAddr}</span
										>
										<span class="muted shrink-0 text-[11px]">{listTime(hit.date)}</span>
									</div>
									<div class="mt-0.5 flex items-center gap-1.5">
										<span
											class="shrink-0 rounded bg-zinc-100 px-1 text-[10px] font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"
											>{hit.folderName || '邮件'}</span
										>
										<span
											class="min-w-0 flex-1 truncate text-[12px] text-zinc-700 dark:text-zinc-200"
											>{hit.subject || '(无主题)'}</span
										>
									</div>
									<p class="muted mt-0.5 line-clamp-1 text-[11px]">{hit.preview}</p>
								</button>
							</li>
						{/each}
					</ul>
				{/if}
			{:else if loading}
				<div class="p-3"><Skeleton rows={6} /></div>
			{:else if conversations.length === 0 && !composing}
				<EmptyState icon="mail" title="没有邮件" description="这个文件夹是空的。" />
			{:else}
				<ul class="divide-y divide-zinc-100 dark:divide-zinc-800">
					{#each conversations as conversation (conversation.key)}
						<li>
							<button
								type="button"
								class="block w-full px-3 py-2.5 text-left transition-colors hover:bg-zinc-50 dark:hover:bg-zinc-800/60 {conversation.unread >
								0
									? 'bg-brand-50/40 dark:bg-brand-500/5'
									: ''} {selected?.key === conversation.key
									? 'bg-zinc-100 dark:bg-zinc-800'
									: ''}"
								onclick={() => open(conversation)}
							>
								<div class="flex items-baseline gap-2">
									{#if conversation.unread > 0}
										<span class="h-1.5 w-1.5 shrink-0 rounded-full bg-brand-600"></span>
									{/if}
									<span
										class="min-w-0 flex-1 truncate text-[13px] {conversation.unread > 0
											? 'font-semibold'
											: 'font-medium'}">{conversation.from.name || conversation.from.address}</span
									>
									<span class="muted shrink-0 text-[11px]">{listTime(conversation.lastAt)}</span>
								</div>
								<div class="mt-0.5 flex items-center gap-1.5">
									<span
										class="min-w-0 flex-1 truncate text-[12px] {conversation.unread > 0
											? 'text-zinc-800 dark:text-zinc-100'
											: 'text-zinc-600 dark:text-zinc-300'}">{conversation.subject ||
										'(无主题)'}</span
									>
									{#if conversation.hasAttachments}<Icon name="paperclip" size={12} />{/if}
									{#if conversation.count > 1}
										<span
											class="rounded bg-zinc-100 px-1 text-[10px] font-medium text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"
										>
											{conversation.count}
										</span>
									{/if}
								</div>
								<p class="muted mt-0.5 line-clamp-1 text-[11px]">{conversation.preview}</p>
							</button>
						</li>
					{/each}
				</ul>
			{/if}
		</div>

		<div class="shrink-0 border-t border-zinc-200 p-2 dark:border-zinc-800">
			<Button variant="ghost" size="sm" class="w-full" onclick={openCompose}>
				<Icon name="edit" size={14} />写邮件
			</Button>
		</div>
	</section>

	<!-- Reader: one thread view, with the composer docked at the bottom -->
	<section
		class="surface min-h-[60vh] min-w-0 flex-col overflow-hidden lg:min-h-0 {selected || draft
			? 'flex'
			: 'hidden lg:flex'}"
	>
		{#if !selected && !draft}
			<div class="flex flex-1 items-center justify-center">
				<EmptyState icon="mail" title="选择一封邮件" description="会话会在这里展开。" />
			</div>
		{:else}
			{#if composing}
				<header
					class="flex h-12 shrink-0 items-center gap-2 border-b border-zinc-200 px-3 dark:border-zinc-800"
				>
					<button
						type="button"
						class="{iconButton} lg:hidden"
						onclick={() => closeDraft()}
						aria-label="关闭写信"
					>
						<Icon name="arrowLeft" size={16} />
					</button>
					<div class="min-w-0 flex-1">
						<p class="truncate text-[13px] font-semibold">
							{draft?.subject.trim() || '新邮件'}
						</p>
						<p class="muted truncate text-[11px]">{draftRecipientSummary}</p>
					</div>
				</header>

				<div class="min-h-0 flex-1 overflow-y-auto px-4 py-4">
					<p class="muted text-center text-[12px]">
						这是一封新邮件，在下面的输入框里填写收件人、主题和正文。
					</p>
				</div>
			{:else if selected}
				<header
					class="flex h-12 shrink-0 items-center gap-2 border-b border-zinc-200 px-3 dark:border-zinc-800"
				>
					<button
						type="button"
						class="{iconButton} lg:hidden"
						onclick={closeReader}
						aria-label="返回列表"
					>
						<Icon name="arrowLeft" size={16} />
					</button>
				<div class="min-w-0 flex-1">
					<p class="truncate text-[13px] font-semibold">{selected.subject || '(无主题)'}</p>
					<p class="muted flex min-w-0 items-center gap-1.5 text-[11px]">
						{#if counterpart}
							<span class="inline-flex shrink-0 items-center gap-1">
								<Icon name="user" size={11} />
								<span
									class="max-w-44 truncate font-medium text-zinc-600 dark:text-zinc-300"
									>{counterpart.name || counterpart.address}</span
								>
							</span>
							{#if counterpart.name && counterpart.address}
								<span class="min-w-0 truncate opacity-70">&lt;{counterpart.address}&gt;</span>
							{/if}
							<span class="shrink-0 opacity-40">·</span>
						{/if}
						<span class="shrink-0">{selected.count} 封 · {relativeTime(selected.lastAt)}</span>
					</p>
				</div>
			</header>

<div bind:this={messagesEl} class="min-h-0 flex-1 overflow-y-auto px-4 py-4">
				{#if loadingThread}
					<Skeleton rows={5} />
				{:else}
					<div class="space-y-4">
						{#each messages as message (message.id)}
							{#if isOutgoing(message)}
								<!-- 我发送的：靠右，信息备注靠右，时间在最右端 -->
								<div
									class="ml-auto flex w-fit max-w-[88%] flex-col items-end"
									title={messageDetails(message)}
								>
									{@render messageBody(message, 'rounded-tr-md')}
									<div
										class="mt-1 flex w-full min-w-0 items-center justify-end gap-2 px-1 text-[11px] leading-4"
									>
										{#if multiSender}
											<span class="muted min-w-0 truncate">{senderLabel(message)}</span>
										{/if}
										<time
											class="muted tabular shrink-0"
											datetime={new Date(message.date * 1000).toISOString()}
											>{messageTime(message.date)}</time
										>
									</div>
								</div>
							{:else}
								<!-- 收到的：靠左，时间在左，昵称/邮箱跟在后面 -->
								<div
									class="flex w-fit max-w-[88%] flex-col items-start"
									title={messageDetails(message)}
								>
									{@render messageBody(message, 'rounded-tl-md')}
									<div
										class="mt-1 flex w-full min-w-0 items-center gap-2 px-1 text-[11px] leading-4"
									>
										<time
											class="muted tabular shrink-0"
											datetime={new Date(message.date * 1000).toISOString()}
											>{messageTime(message.date)}</time
										>
										{#if multiSender}
											<span class="muted min-w-0 truncate">{senderLabel(message)}</span>
										{/if}
									</div>
								</div>
							{/if}
						{/each}
					</div>
				{/if}
				</div>

			{/if}

			{#if draft}
				<footer
					class="mt-auto shrink-0 border-t border-zinc-200 bg-zinc-50/70 px-3 py-2.5 dark:border-zinc-800 dark:bg-zinc-900/40"
				>
					<div class="mb-1.5 flex items-center gap-1.5">
						<span class="muted inline-flex shrink-0 items-center gap-1 text-[11px] font-medium">
							{#if composing}
								<Icon name="edit" size={12} />写邮件
							{:else}
								<Icon name="arrowLeft" size={12} class="rotate-180" />回复
							{/if}
						</span>
						<button
							type="button"
							class="inline-flex min-w-0 max-w-64 items-center gap-1.5 rounded-full border border-zinc-200 bg-white px-2.5 py-1 text-[11px] font-medium text-zinc-600 transition-colors hover:border-brand-300 hover:text-brand-700 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-300 dark:hover:border-brand-500/50 dark:hover:text-brand-300"
							onclick={openRecipients}
							title={addressList([draft.to, draft.cc, draft.bcc].filter(Boolean).join(', '))}
						>
							<Icon name="users" size={12} class="shrink-0" />
							<span class="truncate">{draftRecipientSummary}</span>
							<Icon name="edit" size={11} class="shrink-0 opacity-60" />
						</button>
						<div class="ml-auto flex shrink-0 items-center gap-1">
							<Button size="sm" onclick={sendDraft} disabled={draft.busy || !draftCanSend}>
								{draft.busy ? '发送中…' : '发送'}
							</Button>
						</div>
					</div>
					{#if composing}
						<input
							class="mb-1.5 h-8 w-full rounded-lg border border-zinc-200 bg-white px-2.5 text-[12px] text-zinc-800 placeholder:text-zinc-400 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:focus:ring-brand-500/20"
							placeholder="主题"
							bind:value={draft.subject}
							oninput={scheduleComposeSave}
							maxlength="500"
						/>
					{/if}
					<textarea
						class="{textarea} !min-h-11 w-full resize-none text-[13px]"
						style="max-height: 40vh"
						rows="2"
						bind:this={draftBodyEl}
						bind:value={draft.body}
						disabled={draft.busy}
						oninput={scheduleComposeSave}
						onkeydown={onDraftKeydown}
						placeholder="写点什么…"
					></textarea>
					<p class="muted mt-1 text-[10px]">
						Ctrl/⌘+Enter 发送 · Enter 换行{composing
							? ` · 草稿只存在本浏览器${composeDraftRestored ? '（已恢复上次内容）' : ''}`
							: ` · 主题 ${draft.subject}`}
					</p>
				</footer>
			{/if}
		{/if}
	</section>
</div>

<Modal
	open={recipientsOpen}
	title={composing ? '收件人' : '回复对象'}
	description="用逗号或空格分隔多个地址；留空表示不发送给该字段。"
	onclose={() => (recipientsOpen = false)}
	width="max-w-lg"
>
	<form id="recipients-form" class="space-y-3" onsubmit={applyRecipients}>
		<Field label="收件人" for="reply-to">
			<textarea
				id="reply-to"
				class={textarea}
				rows="2"
				bind:value={draftTo}
				placeholder="a@example.com, b@example.com"
			></textarea>
		</Field>
		<Field label="抄送" for="reply-cc">
			<textarea
				id="reply-cc"
				class={textarea}
				rows="2"
				bind:value={draftCc}
				placeholder="可选"
			></textarea>
		</Field>
		{#if composing}
			<Field label="密送" for="reply-bcc">
				<textarea
					id="reply-bcc"
					class={textarea}
					rows="2"
					bind:value={draftBcc}
					placeholder="可选，不会出现在收件人的邮件头里"
				></textarea>
			</Field>
		{:else}
			<div class="flex flex-wrap gap-1.5">
				<Button variant="ghost" size="sm" onclick={() => useDraftRecipients(false)}>
					仅发件人
				</Button>
				<Button variant="ghost" size="sm" onclick={() => useDraftRecipients(true)}>
					全部回复
				</Button>
			</div>
		{/if}
	</form>

	{#snippet footer()}
		{#if composing && (draftTo || draftCc || draftBcc)}
			<Button
				variant="ghost"
				size="sm"
				onclick={() => {
					draftTo = draftCc = draftBcc = '';
				}}
			>
				清空
			</Button>
		{/if}
		<Button variant="secondary" size="sm" onclick={() => (recipientsOpen = false)}>取消</Button>
		<Button type="submit" size="sm" form="recipients-form">确定</Button>
	{/snippet}
</Modal>
