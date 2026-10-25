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

	let replyTarget = $state<ReplyTarget | null>(null);
	let replyTo = $state('');
	let replyCc = $state('');
	let replyBody = $state('');
	let replyBusy = $state(false);
	let recipientsOpen = $state(false);
	let draftTo = $state('');
	let draftCc = $state('');

	let toList = $derived(splitAddresses(replyTo));
	let ccList = $derived(splitAddresses(replyCc));
	let recipientCount = $derived(toList.length + ccList.length);

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
	let recipientSummary = $derived(
		recipientCount === 0
			? '选择收件人'
			: recipientCount === 1
				? (toList[0] ?? ccList[0] ?? '')
				: `${toList[0] ?? ccList[0] ?? ''} 等 ${recipientCount} 人`
	);

	let currentFolder = $derived(folders.find((f) => f.id === folderId) ?? null);

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
		selected = conversation;
		openFolderId = folder;
		messages = [];
		loadingThread = true;
		try {
			const res = await mailApi.thread(folder, conversation.key);
			messages = res.messages;
			sentFolderId = res.sentFolderId;
			resetReply(res.replyTarget);
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
		selected = null;
		messages = [];
		replyTarget = null;
		replyBody = '';
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

	function resetReply(target: ReplyTarget | undefined) {
		replyTarget = target ?? null;
		replyTo = target ? joinAddresses(target.to ?? []) : '';
		replyCc = target ? joinAddresses(target.cc ?? []) : '';
		replyBody = '';
	}

	function openRecipients() {
		draftTo = replyTo;
		draftCc = replyCc;
		recipientsOpen = true;
	}

	function applyRecipients(event: SubmitEvent) {
		event.preventDefault();
		replyTo = joinAddresses(splitAddresses(draftTo));
		replyCc = joinAddresses(splitAddresses(draftCc));
		recipientsOpen = false;
	}

	function replyToSender() {
		const sender = replyTarget?.to[0];
		if (!sender) return;
		replyTo = sender;
		replyCc = '';
	}

	function replyToEveryone() {
		if (!replyTarget) return;
		replyTo = joinAddresses(replyTarget.to ?? []);
		replyCc = joinAddresses(replyTarget.cc ?? []);
	}

	async function sendReply() {
		if (!replyTarget || !replyBody.trim() || replyBusy) return;
		if (toList.length === 0) {
			toast.fail('请至少填写一个收件人', '无法发送');
			return;
		}
		replyBusy = true;
		try {
			await mailApi.reply({
				messageId: replyTarget.messageId,
				subject: replyTarget.subject,
				body: replyBody.trim(),
				to: toList,
				cc: ccList
			});
			replyBody = '';
			toast.ok('回复已发送');
			const key = selected?.key;
			await refresh();
			const updated = key ? conversations.find((item) => item.key === key) : undefined;
			if (updated ?? selected)
				await open((updated ?? selected) as Conversation, openFolderId ?? folderId);
		} catch (err) {
			toast.fail(err, '发送失败');
		} finally {
			replyBusy = false;
		}
	}

	function onReplyKeydown(event: KeyboardEvent) {
		// Enter sends, Shift+Enter breaks the line; never send mid-composition.
		if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
			event.preventDefault();
			void sendReply();
		}
	}
</script>

<svelte:head>
	<title>邮件 · lifemail</title>
</svelte:head>

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
		class="surface flex min-h-[60vh] min-w-0 flex-col overflow-hidden lg:min-h-0 {selected
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
			{:else if conversations.length === 0}
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
	</section>

	<!-- Reader -->
	<section
		class="surface min-h-[60vh] min-w-0 flex-col overflow-hidden lg:min-h-0 {selected
			? 'flex'
			: 'hidden lg:flex'}"
	>
		{#if !selected}
			<div class="flex flex-1 items-center justify-center">
				<EmptyState icon="mail" title="选择一封邮件" description="会话会在这里展开。" />
			</div>
		{:else}
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
					<p class="muted text-[11px]">
						{selected.count} 封 · {relativeTime(selected.lastAt)}
					</p>
				</div>
			</header>

			<div bind:this={messagesEl} class="min-h-0 flex-1 overflow-y-auto px-4 py-4">
				{#if loadingThread}
					<Skeleton rows={5} />
				{:else}
					<div class="space-y-4">
						{#each messages as message (message.id)}
							<article
								class="rounded-xl border border-zinc-200 bg-white p-4 shadow-xs dark:border-zinc-800 dark:bg-zinc-900/60"
							>
								<header class="mb-3 flex flex-wrap items-start justify-between gap-2">
									<div class="min-w-0">
										<p class="text-[13px] font-semibold">
											{message.fromName || message.fromAddr}
											{#if message.folderId === sentFolderId}
												<span class="muted ml-1 text-[10px] font-normal">已发送</span>
											{/if}
										</p>
										<p class="muted truncate text-[11px]">{message.fromAddr}</p>
										<p class="muted mt-0.5 truncate text-[11px]" title={message.to}>
											收件人：{addressList(message.to)}
										</p>
									</div>
									<time class="muted shrink-0 text-[11px]" title={dateTime(message.date)}
										>{dateTime(message.date)}</time
									>
								</header>

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
														<span class="muted shrink-0 text-[10px]"
															>{bytes(attachment.size)}</span
														>
													</a>
												</li>
											{/each}
										</ul>
									</div>
								{/if}
							</article>
						{/each}
					</div>
				{/if}
			</div>

			<footer
				class="shrink-0 border-t border-zinc-200 bg-zinc-50/70 px-3 py-2.5 dark:border-zinc-800 dark:bg-zinc-900/40"
			>
				{#if replyTarget}
					<div class="mb-2 flex items-center gap-1.5">
						<span class="muted inline-flex shrink-0 items-center gap-1 text-[11px] font-medium">
							<Icon name="arrowLeft" size={12} class="rotate-180" />回复
						</span>
						<button
							type="button"
							class="inline-flex min-w-0 max-w-72 items-center gap-1.5 rounded-full border border-zinc-200 bg-white px-2.5 py-1 text-[11px] font-medium text-zinc-600 transition-colors hover:border-brand-300 hover:text-brand-700 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-300 dark:hover:border-brand-500/50 dark:hover:text-brand-300"
							onclick={openRecipients}
							title={addressList([replyTo, replyCc].filter(Boolean).join(', '))}
						>
							<Icon name="users" size={12} class="shrink-0" />
							<span class="truncate">{recipientSummary}</span>
							<Icon name="edit" size={11} class="shrink-0 opacity-60" />
						</button>
						<div class="ml-auto flex shrink-0 items-center gap-1">
							<Button variant="ghost" size="sm" onclick={replyToSender}>仅发件人</Button>
							<Button variant="ghost" size="sm" onclick={replyToEveryone}>全部回复</Button>
						</div>
					</div>
					<div class="flex items-end gap-2">
						<textarea
							class="{textarea} max-h-40 min-h-11 flex-1 resize-none text-[13px]"
							rows="2"
							bind:value={replyBody}
							disabled={replyBusy}
							placeholder="写点什么…"
							onkeydown={onReplyKeydown}
						></textarea>
						<Button size="sm" onclick={sendReply} disabled={replyBusy || !replyBody.trim()}>
							{replyBusy ? '发送中…' : '发送'}
						</Button>
					</div>
					<p class="muted mt-1 text-[10px]">
						Enter 发送 · Shift+Enter 换行 · 主题 {replyTarget.subject}
					</p>
				{:else}
					<p class="muted text-[11px]">这个会话没有可回复的地址。</p>
				{/if}
			</footer>
		{/if}
	</section>
</div>

<Modal
	open={recipientsOpen}
	title="回复对象"
	description="用逗号或空格分隔多个地址；留空表示不抄送。"
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
		<div class="flex flex-wrap gap-1.5">
			<Button
				variant="ghost"
				size="sm"
				onclick={() => {
					draftTo = replyTarget?.to[0] ?? '';
					draftCc = '';
				}}
			>
				仅发件人
			</Button>
			<Button
				variant="ghost"
				size="sm"
				onclick={() => {
					draftTo = joinAddresses(replyTarget?.to ?? []);
					draftCc = joinAddresses(replyTarget?.cc ?? []);
				}}
			>
				全部回复
			</Button>
		</div>
	</form>

	{#snippet footer()}
		<Button variant="secondary" size="sm" onclick={() => (recipientsOpen = false)}>取消</Button>
		<Button type="submit" size="sm" form="recipients-form">确定</Button>
	{/snippet}
</Modal>
