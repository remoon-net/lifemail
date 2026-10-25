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
	import { input, textarea } from '#lib/ui';
	import type { CalendarEvent, CalendarRow, EventInput } from '#lib/mail-types';

	const DAY = 86_400;
	const RANGE_DAYS = 30;

	type EventDraft = {
		subject: string;
		location: string;
		body: string;
		allDay: boolean;
		start: string;
		end: string;
	};

	let calendars = $state<CalendarRow[]>([]);
	let selectedId = $state<number | null>(null);
	let events = $state<CalendarEvent[]>([]);
	let loading = $state(true);
	let loadingEvents = $state(false);
	let busy = $state(false);

	let modalOpen = $state(false);
	let modalMode = $state<'create' | 'rename'>('create');
	let nameDraft = $state('');

	let eventModal = $state(false);
	let editing = $state<CalendarEvent | null>(null);
	let eventBusy = $state(false);
	let eventDraft = $state<EventDraft>({
		subject: '',
		location: '',
		body: '',
		allDay: false,
		start: '',
		end: ''
	});

	let readOnly = $derived(Boolean(editing?.recurring));

	let selected = $derived(calendars.find((c) => c.id === selectedId) ?? null);

	function pad(value: number): string {
		return String(value).padStart(2, '0');
	}

	function toLocalInput(date: Date): string {
		return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(
			date.getHours()
		)}:${pad(date.getMinutes())}`;
	}

	function toDateInput(date: Date): string {
		return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
	}

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

	function openCreateEvent() {
		if (!selected) return;
		const start = new Date();
		start.setMinutes(0, 0, 0);
		start.setHours(start.getHours() + 1);
		editing = null;
		eventDraft = {
			subject: '',
			location: '',
			body: '',
			allDay: false,
			start: toLocalInput(start),
			end: toLocalInput(new Date(start.getTime() + 3_600_000))
		};
		eventModal = true;
	}

	function openEditEvent(event: CalendarEvent) {
		editing = event;
		const start = new Date(event.start * 1000);
		// All-day events store the exclusive next-midnight end, so show the
		// last day the event covers.
		const end = new Date(Math.max(event.end, event.start + DAY) * 1000);
		eventDraft = {
			subject: event.subject,
			location: event.location,
			body: event.body,
			allDay: event.allDay,
			start: event.allDay ? toDateInput(start) : toLocalInput(start),
			end: event.allDay ? toDateInput(new Date(end.getTime() - DAY * 1000)) : toLocalInput(end)
		};
		eventModal = true;
	}

	function setAllDay(value: boolean) {
		if (value === eventDraft.allDay) return;
		if (value) {
			eventDraft = {
				...eventDraft,
				allDay: true,
				start: toDateInput(new Date(eventDraft.start)),
				end: toDateInput(new Date(eventDraft.end))
			};
			return;
		}
		eventDraft = {
			...eventDraft,
			allDay: false,
			start: toLocalInput(new Date(`${eventDraft.start}T09:00`)),
			end: toLocalInput(new Date(`${eventDraft.end}T10:00`))
		};
	}

	async function submitEvent(event: SubmitEvent) {
		event.preventDefault();
		if (readOnly || !selectedId) return;
		const subject = eventDraft.subject.trim();
		if (!subject) return;

		let start: number;
		let end: number;
		if (eventDraft.allDay) {
			const startDay = new Date(`${eventDraft.start}T00:00:00`);
			const endDay = new Date(`${eventDraft.end}T00:00:00`);
			start = Math.floor(startDay.getTime() / 1000);
			// All-day ends are exclusive: the next midnight of the last day.
			end = Math.floor(
				new Date(endDay.getFullYear(), endDay.getMonth(), endDay.getDate() + 1).getTime() / 1000
			);
		} else {
			start = Math.floor(new Date(eventDraft.start).getTime() / 1000);
			end = Math.floor(new Date(eventDraft.end).getTime() / 1000);
		}
		if (!Number.isFinite(start) || !Number.isFinite(end)) {
			toast.push('请填写有效的开始与结束时间', 'error');
			return;
		}
		if (end < start) {
			toast.push('结束时间不能早于开始时间', 'error');
			return;
		}

		const body: EventInput = {
			subject,
			location: eventDraft.location.trim(),
			body: eventDraft.body.trim(),
			start,
			end,
			allDay: eventDraft.allDay
		};
		eventBusy = true;
		try {
			if (editing) {
				await mailApi.updateEvent(selectedId, editing.id, body);
			} else {
				await mailApi.createEvent(selectedId, { ...body, reminder: 15 });
			}
			eventModal = false;
			await Promise.all([loadEvents(), loadCalendars()]);
			toast.ok(editing ? '事件已更新' : '事件已创建');
		} catch (err) {
			toast.fail(err, editing ? '更新失败' : '创建失败');
		} finally {
			eventBusy = false;
		}
	}

	async function deleteEvent() {
		if (readOnly || !editing || !selectedId) return;
		if (!window.confirm(`删除事件“${editing.subject || '(无主题)'}”？`)) return;
		eventBusy = true;
		try {
			await mailApi.deleteEvent(selectedId, editing.id);
			eventModal = false;
			await Promise.all([loadEvents(), loadCalendars()]);
			toast.ok('事件已删除');
		} catch (err) {
			toast.fail(err, '删除失败');
		} finally {
			eventBusy = false;
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
		<div class="min-h-0 flex-1 overflow-y-auto p-2">
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
				<div class="flex flex-wrap items-center gap-1.5">
					<Button size="sm" onclick={openCreateEvent}>
						<Icon name="plus" size={13} />新建事件
					</Button>
					<Button variant="ghost" size="sm" onclick={openRename}>
						<Icon name="edit" size={13} />重命名
					</Button>
					<Button
						variant={selected.public ? 'secondary' : 'ghost'}
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
						description="点击「新建事件」添加，或由客户端同步。"
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
										<li>
											<button
												type="button"
												class="flex w-full gap-3 rounded-lg border border-zinc-200 bg-white px-3 py-2.5 text-left transition-colors hover:border-brand-300 hover:bg-brand-50/30 dark:border-zinc-800 dark:bg-zinc-900/60 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/5"
												onclick={() => openEditEvent(event)}
												title={event.recurring ? '查看重复事件' : '编辑事件'}
											>
												<span class="mt-1.5 h-4 w-0.5 shrink-0 rounded bg-brand-500"></span>
												<span class="min-w-0 flex-1">
													<span class="flex flex-wrap items-center gap-2">
														<span class="text-[13px] font-medium">{event.subject || '(无主题)'}</span>
														{#if event.recurring}
															<Badge tone="brand">重复</Badge>
															<span class="muted text-[10px]">客户端编辑</span>
														{/if}
													</span>
													<span class="muted mt-0.5 block text-[11px]">{timeRange(event)}</span>
													{#if event.location}
														<span class="muted mt-0.5 block truncate text-[11px]"
															>地点：{event.location}</span
														>
													{/if}
													{#if event.organizer || event.organizerAddress}
														<span class="muted mt-0.5 block truncate text-[11px]">
															组织者：{event.organizer || event.organizerAddress}
														</span>
													{/if}
												</span>
												<Icon name={event.recurring ? 'eye' : 'edit'} size={13} class="mt-1 shrink-0 text-zinc-300" />
											</button>
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

<Modal
	open={eventModal}
	title={editing?.recurring ? '查看重复事件' : editing ? '编辑事件' : '新建事件'}
	description={editing?.recurring
		? '重复事件的规则与单次实例由 ActiveSync 客户端管理。'
		: '事件会同步到所有 ActiveSync 客户端。'}
	onclose={() => (eventModal = false)}
	width="max-w-xl"
>
	<form id="event-form" class="space-y-3" onsubmit={submitEvent}>
		{#if readOnly}
			<div
				class="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-[12px] text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300"
			>
				<Icon name="alert" size={14} class="mt-0.5 shrink-0" />
				<span>这是重复事件，请在 ActiveSync 客户端中编辑；此处仅可查看。</span>
			</div>
		{/if}
		<Field label="标题" for="event-subject">
			<input
				id="event-subject"
				class={input}
				bind:value={eventDraft.subject}
				placeholder="例如：产品评审"
				maxlength="255"
				readonly={readOnly}
				required
			/>
		</Field>

		<label class="flex items-center gap-2 text-xs text-zinc-600 dark:text-zinc-400">
			<input
				type="checkbox"
				class="h-4 w-4 rounded border-zinc-300 text-brand-600 focus:ring-brand-500/30 dark:border-zinc-600 dark:bg-zinc-900"
				checked={eventDraft.allDay}
				disabled={readOnly}
				onchange={(e) => setAllDay(e.currentTarget.checked)}
			/>
			全天事件
		</label>

		<div class="grid gap-3 sm:grid-cols-2">
			<Field label="开始" for="event-start">
				<input
					id="event-start"
					class={input}
					type={eventDraft.allDay ? 'date' : 'datetime-local'}
					bind:value={eventDraft.start}
					readonly={readOnly}
					required
				/>
			</Field>
			<Field label="结束" for="event-end">
				<input
					id="event-end"
					class={input}
					type={eventDraft.allDay ? 'date' : 'datetime-local'}
					bind:value={eventDraft.end}
					readonly={readOnly}
					required
				/>
			</Field>
		</div>

		<Field label="地点" for="event-location">
			<input
				id="event-location"
				class={input}
				bind:value={eventDraft.location}
				maxlength="255"
				readonly={readOnly}
			/>
		</Field>

		<Field label="备注" for="event-body">
			<textarea
				id="event-body"
				class={textarea}
				rows="3"
				bind:value={eventDraft.body}
				readonly={readOnly}
			></textarea>
		</Field>
	</form>
	{#snippet footer()}
		{#if readOnly}
			<Button variant="secondary" size="sm" onclick={() => (eventModal = false)}>关闭</Button>
		{:else}
			{#if editing}
				<Button variant="danger" size="sm" class="mr-auto" onclick={deleteEvent} disabled={eventBusy}>
					<Icon name="trash" size={13} />删除
				</Button>
			{/if}
			<Button variant="secondary" size="sm" onclick={() => (eventModal = false)}>取消</Button>
			<Button type="submit" size="sm" form="event-form" disabled={eventBusy}>
				{editing ? '保存' : '创建'}
			</Button>
		{/if}
	{/snippet}
</Modal>
