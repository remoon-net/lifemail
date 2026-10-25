<script lang="ts">
	import { onMount } from 'svelte';
	import Badge from '#lib/components/Badge.svelte';
	import Card from '#lib/components/Card.svelte';
	import CopyButton from '#lib/components/CopyButton.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import { api } from '#lib/api';
	import { toast } from '#lib/toast.svelte';
	import type { CalendarRow } from '#lib/types';

	let rows = $state<CalendarRow[]>([]);
	let loading = $state(true);
	let busy = $state<number | null>(null);

	async function load() {
		loading = true;
		try {
			rows = (await api.calendars()).items;
		} catch (err) {
			toast.fail(err, '无法读取日历');
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void load();
	});

	async function toggle(row: CalendarRow) {
		busy = row.id;
		try {
			const result = await api.setCalendarPublic(row.id, !row.public);
			rows = rows.map((item) => (item.id === row.id ? { ...item, public: result.public } : item));
			toast.ok(result.public ? '日历已公开' : '日历已取消公开');
		} catch (err) {
			toast.fail(err, '修改失败');
		} finally {
			busy = null;
		}
	}
</script>

<div class="space-y-4">
	<p class="muted text-xs">
		公开日历会生成一个固定 URL 的 <code class="font-mono">.ics</code>，任何人拿到链接都能订阅，无需登录。
		链接中的 UUID 本身就是凭据，取消公开后立即失效。
	</p>

	<Card padding="none">
		{#if loading}
			<div class="p-4"><Skeleton rows={3} /></div>
		{:else if rows.length === 0}
			<EmptyState icon="calendar" title="还没有日历" description="账号创建时会自动生成一个日历。" />
		{:else}
			<ul class="divide-y divide-zinc-100 dark:divide-zinc-800">
				{#each rows as row (row.id)}
					<li class="flex flex-wrap items-center gap-3 px-4 py-3">
						<span
							class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-brand-50 text-brand-600 dark:bg-brand-500/15 dark:text-brand-300"
						>
							<Icon name="calendar" size={16} />
						</span>
						<div class="min-w-0 flex-1">
							<div class="flex flex-wrap items-center gap-2">
								<span class="text-[13px] font-medium">{row.name}</span>
								<span class="muted text-xs">{row.owner}</span>
								{#if row.public}
									<Badge tone="ok">公开</Badge>
								{:else}
									<Badge>未公开</Badge>
								{/if}
							</div>
							{#if row.public && row.subscribe_url}
								<p class="muted mt-1 truncate font-mono text-[11px]">{row.subscribe_url}</p>
							{/if}
						</div>
						<div class="flex shrink-0 items-center gap-2">
							{#if row.public && row.subscribe_url}
								<CopyButton value={row.subscribe_url} label="复制链接" />
							{/if}
							<button
								type="button"
								role="switch"
								aria-checked={row.public}
								aria-label={`${row.owner} 的 ${row.name} 公开订阅`}
								disabled={busy === row.id}
								class="relative inline-flex h-5.5 w-10 shrink-0 items-center rounded-full transition-colors disabled:opacity-50 {row.public
									? 'bg-brand-600 dark:bg-brand-500'
									: 'bg-zinc-300 dark:bg-zinc-700'}"
								onclick={() => toggle(row)}
							>
								<span
									class="inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform {row.public
										? 'translate-x-5'
										: 'translate-x-1'}"
								></span>
							</button>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</Card>
</div>
