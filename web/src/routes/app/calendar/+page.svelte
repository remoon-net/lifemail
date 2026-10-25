<script lang="ts">
	import { onMount } from 'svelte';
	import Badge from '#lib/components/Badge.svelte';
	import Button from '#lib/components/Button.svelte';
	import CopyButton from '#lib/components/CopyButton.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import Field from '#lib/components/Field.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import Modal from '#lib/components/Modal.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import { mailApi } from '#lib/mail-api';
	import { toast } from '#lib/toast.svelte';
	import { input } from '#lib/ui';
	import type { CalendarEvent, CalendarRow } from '#lib/mail-types';

	const DAY = 86_400;
	const RANGE_DAYS = 30;

	let calendars = $state<CalendarRow[]>([]);
	let selectedId = $state<number | null>(null);
	let events = $state<CalendarEvent[]>([]);
	let loading = $state(true);
	let loadingEvents = $state(false);
	let busy = $state(false);

	let modalOpen = $state(false);
	let modalMode = $state<'create' | 'rename'>('create');
	let nameDraft = $state('');

	let selected = $derived(calendars.find((c) => c.id === selectedId) ?? null);

	function dayKey(unix: number): string {
		return new Date(unix * 1000).toDateString();
	}

	function dayLabel(unix: number): string {
		return new Date(unix * 1000).toLocaleDateString('zh-CN', {
			month: 'long',
			day: 'numeric',
			weekday: 'short'
		});
	}

	function timeRange(event: CalendarEvent): string {
		if (event.allDay) return '全天';
		const start = new Date(event.start * 1000);
		const end = new Date(event.end * 1000);
		const hhmm = (d: Date) =>
			d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false });
		return `${hhmm(start)} – ${hhmm(end)}`;
	}

	let groups = $derived.by(() => {
		const map = new Map<string, { label: string; items: CalendarEvent[] }>();
		for (const event of events) {
			const key = dayKey(event.start);
			const bucket = map.get(key);
			if (bucket) bucket.items.push(event);
			else map.set(key, { label: dayLabel(event.start), items: [event] });
		}
		return [...map.values()];
	});

	async function loadCalendars() {
		const res = await mailApi.calendars();
		calendars = res.calendars;
		selectedId ??= calendars[0]?.id ?? null;
	}

	async function loadEvents() {
		if (!selectedId) {
			events = [];
			return;
		}
		loadingEvents = true;
		try {
			const from = Math.floor(new Date().setHours(0, 0, 0, 0) / 1000);
			const res = await mailApi.calendarEvents(selectedId, from, from + RANGE_DAYS * DAY);
			events = res.events;
		} catch (err) {
			toast.fail(err, '无法读取事件');
		} finally {
			loadingEvents = false;
		}
	}

	onMount(async () => {
		try {
			await loadCalendars();
			await loadEvents();
		} catch (err) {
			toast.fail(err, '无法读取日历');
		} finally {
			loading = false;
		}
	});

	async function choose(id: number) {
		if (id === selectedId) return;
		selectedId = id;
		events = [];
		await loadEvents();
	}

	function openCreate() {
		modalMode = 'create';
		nameDraft = '';
		modalOpen = true;
	}

	function openRename() {
		if (!selected) return;
		modalMode = 'rename';
		nameDraft = selected.name;
		modalOpen = true;
	}

	async function submitModal(event: SubmitEvent) {
		event.preventDefault();
		const name = nameDraft.trim();
		if (!name) return;
		busy = true;
		try {
			if (modalMode === 'create') {
				const res = await mailApi.createCalendar(name);
				calendars = [...calendars, res.calendar];
				selectedId = res.calendar.id;
				events = [];
				await loadEvents();
				toast.ok('日历已创建');
			} else if (selected) {
				const res = await mailApi.renameCalendar(selected.id, name);
				calendars = calendars.map((c) => (c.id === res.calendar.id ? res.calendar : c));
				toast.ok('日历已重命名');
			}
			modalOpen = false;
		} catch (err) {
			toast.fail(err, modalMode === 'create' ? '创建失败' : '重命名失败');
		} finally {
			busy = false;
		}
	}

	async function togglePublic() {
		if (!selected) return;
		busy = true;
		try {
			const res = await mailApi.setCalendarPublic(selected.id, !selected.public);
			calendars = calendars.map((c) => (c.id === res.calendar.id ? res.calendar : c));
			toast.ok(res.calendar.public ? '日历已公开' : '已取消公开');
		} catch (err) {
			toast.fail(err, '修改失败');
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>日历 · lifemail</title>
</svelte:head>

<div class="lg:grid lg:h-[calc(100dvh-9rem)] lg:grid-cols-[16rem_minmax(0,1fr)] lg:gap-4">
	<!-- Calendar list -->
	<section class="surface mb-4 flex min-h-[50vh] flex-col overflow-hidden lg:mb-0 lg:min-h-0">
		<header
			class="flex h-12 items-center gap-2 border-b border-zinc-200 px-3 dark:border-zinc-800"
		>
			<p class="min-w-0 flex-1 text-[13px] font-semibold">我的日历</p>
			<Button variant="secondary" size="sm" onclick={openCreate}>
				<Icon name="plus" size={13} />新建
			</Button>
		</header>
		<div class="p-2">
			{#if loading}
				<div class="p-2"><Skeleton rows={3} /></div>
			{:else if calendars.length === 0}
				<EmptyState icon="calendar" title="还没有日历" description="新建一个日历开始使用。" />
			{:else}
				<ul class="space-y-0.5">
					{#each calendars as calendar (calendar.id)}
						<li>
							<button
								type="button"
								class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left transition-colors {calendar.id ===
								selectedId
									? 'bg-brand-50 dark:bg-brand-500/15'
									: 'hover:bg-zinc-100 dark:hover:bg-zinc-800'}"
								onclick={() => choose(calendar.id)}
							>
								<span
									class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-brand-600/10 text-brand-600 dark:bg-brand-500/20 dark:text-brand-300"
								>
									<Icon name="calendar" size={14} />
								</span>
								<span class="min-w-0 flex-1">
									<span class="block truncate text-[13px] font-medium">{calendar.name}</span>
									<span class="muted block text-[11px]">{calendar.eventCount} 个事件</span>
								</span>
								{#if calendar.public}<Badge tone="ok">公开</Badge>{/if}
							</button>
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	</section>

	<!-- Selected calendar -->
		<section class="surface flex min-h-[50vh] flex-col overflow-hidden lg:min-h-0">
		{#if !selected}
			<div class="flex flex-1 items-center justify-center">
				<EmptyState icon="calendar" title="选择一个日历" description="事件会显示在这里。" />
			</div>
		{:else}
			<header
				class="flex flex-wrap items-center gap-2 border-b border-zinc-200 px-4 py-3 dark:border-zinc-800"
			>
				<div class="min-w-0 flex-1">
					<div class="flex items-center gap-2">
						<h2 class="truncate text-sm font-semibold">{selected.name}</h2>
						{#if selected.public}<Badge tone="ok">公开</Badge>{/if}
					</div>
					<p class="muted text-xs">未来 {RANGE_DAYS} 天 · {events.length} 个事件</p>
				</div>
				<div class="flex items-center gap-1.5">
					<Button variant="ghost" size="sm" onclick={openRename}>
						<Icon name="edit" size={13} />重命名
					</Button>
					<Button
						variant={selected.public ? 'secondary' : 'primary'}
						size="sm"
						disabled={busy}
						onclick={togglePublic}
					>
						<Icon name={selected.public ? 'x' : 'external'} size={13} />
						{selected.public ? '取消公开' : '公开订阅'}
					</Button>
				</div>
			</header>

			{#if selected.public && selected.subscribeUrl}
				<div
					class="flex flex-wrap items-center gap-2 border-b border-zinc-200 bg-zinc-50/60 px-4 py-2 dark:border-zinc-800 dark:bg-zinc-900/40"
				>
					<code class="min-w-0 flex-1 truncate font-mono text-[11px]">{selected.subscribeUrl}</code>
					<CopyButton value={selected.subscribeUrl} label="复制订阅链接" />
				</div>
			{/if}

			<div class="min-h-0 flex-1 overflow-y-auto p-4">
				{#if loadingEvents}
					<Skeleton rows={5} />
				{:else if groups.length === 0}
					<EmptyState
						icon="calendar"
						title="近期没有事件"
						description="事件由手机或桌面客户端通过 ActiveSync 写入。"
					/>
				{:else}
					<div class="space-y-5">
						{#each groups as group (group.label)}
							<div>
								<p
									class="mb-2 text-[11px] font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500"
								>
									{group.label}
								</p>
								<ul class="space-y-1.5">
									{#each group.items as event (event.id)}
										<li
											class="flex gap-3 rounded-lg border border-zinc-200 bg-white px-3 py-2.5 dark:border-zinc-800 dark:bg-zinc-900/60"
										>
											<span class="mt-1.5 h-4 w-0.5 shrink-0 rounded bg-brand-500"></span>
											<div class="min-w-0 flex-1">
												<div class="flex flex-wrap items-center gap-2">
													<p class="text-[13px] font-medium">{event.subject || '(无主题)'}</p>
													{#if event.recurring}<Badge tone="brand">重复</Badge>{/if}
												</div>
												<p class="muted mt-0.5 text-[11px]">{timeRange(event)}</p>
												{#if event.location}
													<p class="muted mt-0.5 truncate text-[11px]">地点：{event.location}</p>
												{/if}
												{#if event.organizer || event.organizerAddress}
													<p class="muted mt-0.5 truncate text-[11px]">
														组织者：{event.organizer || event.organizerAddress}
													</p>
												{/if}
											</div>
										</li>
									{/each}
								</ul>
							</div>
						{/each}
					</div>
				{/if}
			</div>
		{/if}
	</section>
</div>

<Modal
	open={modalOpen}
	title={modalMode === 'create' ? '新建日历' : '重命名日历'}
	description={modalMode === 'create'
		? '日历会同步到所有 ActiveSync 客户端。'
		: '修改名称不会改变公开订阅链接。'}
	onclose={() => (modalOpen = false)}
>
	<form id="calendar-form" class="space-y-3" onsubmit={submitModal}>
		<Field label="名称" for="calendar-name">
			<input
				id="calendar-name"
				class={input}
				bind:value={nameDraft}
				placeholder="例如：工作"
				maxlength="100"
				required
			/>
		</Field>
	</form>
	{#snippet footer()}
		<Button variant="secondary" size="sm" onclick={() => (modalOpen = false)}>取消</Button>
		<Button type="submit" size="sm" form="calendar-form" disabled={busy}>
			{modalMode === 'create' ? '创建' : '保存'}
		</Button>
	{/snippet}
</Modal>
