<script lang="ts">
	import { onMount } from 'svelte';
	import Badge from '#lib/components/Badge.svelte';
	import Button from '#lib/components/Button.svelte';
	import Card from '#lib/components/Card.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import Modal from '#lib/components/Modal.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import { api } from '#lib/api';
	import { bytes, dateTime, relativeTime } from '#lib/format';
	import { toast } from '#lib/toast.svelte';
	import type { OutboxItem } from '#lib/types';
	import { iconButton, iconButtonDanger } from '#lib/ui';

	type Filter = 'all' | 'pending' | 'failed';

	let items = $state<OutboxItem[]>([]);
	let loading = $state(true);
	let refreshing = $state(false);
	let filter = $state<Filter>('all');
	let dropping = $state<OutboxItem | null>(null);
	let timer: ReturnType<typeof setInterval> | undefined;

	async function load(silent = false) {
		if (!silent) loading = true;
		else refreshing = true;
		try {
			const data = await api.outbox(false);
			items = data.items;
		} catch (err) {
			toast.fail(err, '无法读取发件队列');
		} finally {
			loading = false;
			refreshing = false;
		}
	}

	onMount(() => {
		void load();
		timer = setInterval(() => {
			if (!document.hidden) void load(true);
		}, 5000);
		return () => clearInterval(timer);
	});

	let visible = $derived(
		items.filter((item) => {
			if (filter === 'failed') return item.failed;
			if (filter === 'pending') return !item.failed;
			return true;
		})
	);
	let pendingCount = $derived(items.filter((i) => !i.failed).length);
	let failedCount = $derived(items.filter((i) => i.failed).length);

	async function retry(item: OutboxItem) {
		try {
			await api.retryOutbox(item.id);
			await load(true);
			toast.ok(`#${item.id} 已重新排队`);
		} catch (err) {
			toast.fail(err, '重试失败');
		}
	}

	async function drop() {
		if (!dropping) return;
		const item = dropping;
		dropping = null;
		try {
			await api.deleteOutbox(item.id);
			await load(true);
			toast.ok(`#${item.id} 已删除`);
		} catch (err) {
			toast.fail(err, '删除失败');
		}
	}
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-center gap-2">
		<div class="flex items-center gap-1 rounded-lg border border-zinc-200 bg-white p-0.5 dark:border-zinc-800 dark:bg-zinc-900">
			{#each [
				{ key: 'all', label: `全部 ${items.length}` },
				{ key: 'pending', label: `待投递 ${pendingCount}` },
				{ key: 'failed', label: `失败 ${failedCount}` }
			] as tab (tab.key)}
				<button
					type="button"
					class="rounded-md px-2.5 py-1 text-[12px] font-medium transition-colors {filter ===
					tab.key
						? 'bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900'
						: 'text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800'}"
					onclick={() => (filter = tab.key as Filter)}
				>
					{tab.label}
				</button>
			{/each}
		</div>
		<p class="muted ml-auto text-xs">每 5 秒自动刷新</p>
		<Button variant="secondary" size="sm" onclick={() => load(true)} disabled={refreshing}>
			<Icon name="refresh" size={14} class={refreshing ? 'animate-spin' : ''} />刷新
		</Button>
	</div>

	<Card padding="none">
		{#if loading}
			<div class="p-4"><Skeleton rows={4} /></div>
		{:else if visible.length === 0}
			<EmptyState
				icon="mail"
				title={filter === 'failed' ? '没有失败的邮件' : filter === 'pending' ? '队列已清空' : '队列为空'}
				description="外部地址投递失败时会先写入队列，按指数退避重试约 12 小时。"
			/>
		{:else}
			<ul class="divide-y divide-zinc-100 dark:divide-zinc-800">
				{#each visible as item (item.id)}
					<li class="px-4 py-3">
						<div class="flex flex-wrap items-start gap-x-4 gap-y-2">
							<div class="min-w-0 flex-1">
								<div class="flex flex-wrap items-center gap-2">
									<span class="font-mono text-xs text-zinc-400">#{item.id}</span>
									<span class="truncate text-[13px] font-medium">{item.envelope_from}</span>
									<Icon name="arrowRight" size={12} class="text-zinc-400" />
									<span class="muted truncate text-[13px]">{item.recipients.join(', ')}</span>
									{#if item.failed}
										<Badge tone="danger">已失败</Badge>
									{:else}
										<Badge tone="warn">待投递</Badge>
									{/if}
								</div>
								<p class="muted mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px]">
									<span>{bytes(item.size)}</span>
									<span>· 尝试 {item.attempts} 次</span>
									{#if !item.failed && item.next_attempt}
										<span>· 下次 {relativeTime(item.next_attempt)}</span>
									{/if}
									<span>· 入队 {dateTime(item.created_at)}</span>
								</p>
								{#if item.last_error}
									<p class="mt-1.5 rounded-md bg-red-50 px-2 py-1 font-mono text-[11px] break-all text-red-700 dark:bg-red-950/40 dark:text-red-300">
										{item.last_error}
									</p>
								{/if}
							</div>
							<div class="flex shrink-0 items-center gap-0.5">
								<button
									type="button"
									class={iconButton}
									title="重新排队"
									aria-label={`重新排队 #${item.id}`}
									onclick={() => retry(item)}
								>
									<Icon name="refresh" size={15} />
								</button>
								<button
									type="button"
									class={iconButtonDanger}
									title="放弃投递"
									aria-label={`删除 #${item.id}`}
									onclick={() => (dropping = item)}
								>
									<Icon name="trash" size={15} />
								</button>
							</div>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</Card>
</div>

<Modal
	open={dropping !== null}
	title="放弃投递"
	description={dropping ? `删除队列中的邮件 #${dropping.id}？该邮件不会送达。` : ''}
	onclose={() => (dropping = null)}
>
	{#snippet footer()}
		<Button variant="secondary" onclick={() => (dropping = null)}>取消</Button>
		<Button variant="danger" onclick={drop}>删除</Button>
	{/snippet}
</Modal>
