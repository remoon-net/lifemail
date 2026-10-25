<script lang="ts">
	import { onMount, tick } from 'svelte';
	import Icon from '#lib/components/Icon.svelte';
	import MailBody from '#lib/components/MailBody.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import { mailApi } from '#lib/mail-api';
	import { bytes, dateTime, relativeTime } from '#lib/format';
	import { toast } from '#lib/toast.svelte';
	import { iconButton } from '#lib/ui';
	import type { IconName } from '#lib/icons';
	import type { Conversation, MailFolder, MailMessage } from '#lib/mail-types';

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

	async function open(conversation: Conversation) {
		if (!folderId) return;
		selected = conversation;
		messages = [];
		loadingThread = true;
		try {
			const res = await mailApi.thread(folderId, conversation.key);
			messages = res.messages;
			const unread = messages.filter((m) => !m.isRead);
			if (unread.length > 0) {
				await Promise.all(unread.map((m) => mailApi.markRead(m.id)));
				messages = messages.map((m) => ({ ...m, isRead: true }));
				conversations = conversations.map((item) =>
					item.key === conversation.key ? { ...item, unread: 0 } : item
				);
				selected = { ...conversation, unread: 0 };
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
				<p class="truncate text-[13px] font-semibold">{currentFolder?.name ?? '邮件'}</p>
				<p class="muted text-[11px]">{conversations.length} 个会话</p>
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
		</header>

		<div class="min-h-0 flex-1 overflow-y-auto">
			{#if loading}
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
										</p>
										<p class="muted truncate text-[11px]">{message.fromAddr}</p>
										<p class="muted mt-0.5 truncate text-[11px]">收件人：{message.to}</p>
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
		{/if}
	</section>
</div>
