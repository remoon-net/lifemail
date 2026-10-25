<script lang="ts">
	import { onMount } from 'svelte';
	import Button from '#lib/components/Button.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import Field from '#lib/components/Field.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import Modal from '#lib/components/Modal.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import { mailApi } from '#lib/mail-api';
	import { toast } from '#lib/toast.svelte';
	import { iconButton, input, textarea } from '#lib/ui';
	import type { Contact, ContactInput } from '#lib/mail-types';

	type ContactDraft = {
		fileAs: string;
		firstName: string;
		lastName: string;
		company: string;
		department: string;
		jobTitle: string;
		email1: string;
		email2: string;
		email3: string;
		mobilePhone: string;
		businessPhone: string;
		homePhone: string;
		home2Phone: string;
		businessFax: string;
		homeStreet: string;
		homeCity: string;
		homeState: string;
		homePostalCode: string;
		homeCountry: string;
		businessStreet: string;
		businessCity: string;
		businessState: string;
		businessPostalCode: string;
		businessCountry: string;
		body: string;
	};

	const EMPTY: ContactDraft = {
		fileAs: '',
		firstName: '',
		lastName: '',
		company: '',
		department: '',
		jobTitle: '',
		email1: '',
		email2: '',
		email3: '',
		mobilePhone: '',
		businessPhone: '',
		homePhone: '',
		home2Phone: '',
		businessFax: '',
		homeStreet: '',
		homeCity: '',
		homeState: '',
		homePostalCode: '',
		homeCountry: '',
		businessStreet: '',
		businessCity: '',
		businessState: '',
		businessPostalCode: '',
		businessCountry: '',
		body: ''
	};

	let contacts = $state<Contact[]>([]);
	let query = $state('');
	let selected = $state<Contact | null>(null);
	let loading = $state(true);
	let searching = $state(false);
	let timer: ReturnType<typeof setTimeout> | undefined;

	let editorOpen = $state(false);
	let editing = $state<Contact | null>(null);
	let draft = $state<ContactDraft>({ ...EMPTY });
	let saving = $state(false);
	let showAddress = $state(false);

	type ChannelKind = 'email' | 'mobile' | 'business' | 'home' | 'home2' | 'fax';
	type Channel = { id: number; kind: ChannelKind; value: string };

	const CHANNEL_OPTIONS: Array<{ kind: ChannelKind; label: string }> = [
		{ kind: 'email', label: '邮箱' },
		{ kind: 'mobile', label: '手机' },
		{ kind: 'business', label: '工作电话' },
		{ kind: 'home', label: '家庭电话' },
		{ kind: 'home2', label: '家庭电话 2' },
		{ kind: 'fax', label: '传真' }
	];

	let channels = $state<Channel[]>([]);
	let nextChannelId = 1;

	// Flattens the dynamic rows back into the fixed fields the API stores.
	function syncChannels() {
		draft.email1 = '';
		draft.email2 = '';
		draft.email3 = '';
		draft.mobilePhone = '';
		draft.businessPhone = '';
		draft.homePhone = '';
		draft.home2Phone = '';
		draft.businessFax = '';
		let emailIndex = 0;
		for (const channel of channels) {
			const value = channel.value.trim();
			if (!value) continue;
			switch (channel.kind) {
				case 'email':
					if (emailIndex === 0) draft.email1 = value;
					else if (emailIndex === 1) draft.email2 = value;
					else if (emailIndex === 2) draft.email3 = value;
					emailIndex++;
					break;
				case 'mobile':
					draft.mobilePhone = value;
					break;
				case 'business':
					draft.businessPhone = value;
					break;
				case 'home':
					draft.homePhone = value;
					break;
				case 'home2':
					draft.home2Phone = value;
					break;
				case 'fax':
					draft.businessFax = value;
					break;
			}
		}
	}

	function channelsFromDraft(): Channel[] {
		const out: Channel[] = [];
		for (const value of [draft.email1, draft.email2, draft.email3]) {
			if (value.trim()) out.push({ id: nextChannelId++, kind: 'email', value });
		}
		const phones: Array<[ChannelKind, string]> = [
			['mobile', draft.mobilePhone],
			['business', draft.businessPhone],
			['home', draft.homePhone],
			['home2', draft.home2Phone],
			['fax', draft.businessFax]
		];
		for (const [kind, value] of phones) {
			if (value.trim()) out.push({ id: nextChannelId++, kind, value });
		}
		return out;
	}

	function addChannel() {
		if (!canAddChannel) return;
		const used = new Set(channels.map((channel) => channel.kind));
		const emailCount = channels.filter((channel) => channel.kind === 'email').length;
		// Prefer a phone type that is not in use yet; extra emails come last.
		const free = CHANNEL_OPTIONS.find(
			(option) => option.kind !== 'email' && !used.has(option.kind)
		);
		const kind: ChannelKind = free?.kind ?? (emailCount < 3 ? 'email' : 'fax');
		channels = [...channels, { id: nextChannelId++, kind, value: '' }];
	}

	function removeChannel(id: number) {
		channels = channels.filter((channel) => channel.id !== id);
	}

	// EAS contacts hold one value per phone type and up to three emails, so a
	// type that is already used cannot be picked again.
	function limitOf(kind: ChannelKind): number {
		return kind === 'email' ? 3 : 1;
	}

	function optionDisabled(kind: ChannelKind, channel: Channel): boolean {
		if (channel.kind === kind) return false;
		const others = channels.filter(
			(other) => other.kind === kind && other.id !== channel.id
		).length;
		return others >= limitOf(kind);
	}

	let canAddChannel = $derived.by(() => {
		const emailCount = channels.filter((channel) => channel.kind === 'email').length;
		if (emailCount < 3) return true;
		return CHANNEL_OPTIONS.some(
			(option) =>
				option.kind !== 'email' && !channels.some((channel) => channel.kind === option.kind)
		);
	});

	function draftOf(contact: Contact): ContactDraft {
		return {
			fileAs: contact.fileAs,
			firstName: contact.firstName,
			lastName: contact.lastName,
			company: contact.company,
			department: contact.department,
			jobTitle: contact.jobTitle,
			email1: contact.email1,
			email2: contact.email2,
			email3: contact.email3,
			mobilePhone: contact.mobilePhone,
			businessPhone: contact.businessPhone,
			homePhone: contact.homePhone,
			home2Phone: contact.home2Phone,
			businessFax: contact.businessFax,
			homeStreet: contact.homeStreet,
			homeCity: contact.homeCity,
			homeState: contact.homeState,
			homePostalCode: contact.homePostalCode,
			homeCountry: contact.homeCountry,
			businessStreet: contact.businessStreet,
			businessCity: contact.businessCity,
			businessState: contact.businessState,
			businessPostalCode: contact.businessPostalCode,
			businessCountry: contact.businessCountry,
			body: contact.body
		};
	}

	async function load(term: string) {
		searching = true;
		try {
			const res = await mailApi.contacts(term);
			contacts = res.contacts;
			if (selected) {
				const fresh = contacts.find((c) => c.id === selected?.id);
				selected = fresh ?? null;
			}
		} catch (err) {
			toast.fail(err, '无法读取联系人');
		} finally {
			searching = false;
		}
	}

	onMount(async () => {
		await load('');
		loading = false;
	});

	function onQueryInput() {
		clearTimeout(timer);
		timer = setTimeout(() => void load(query.trim()), 250);
	}

	function openCreate() {
		editing = null;
		draft = { ...EMPTY };
		channels = [{ id: nextChannelId++, kind: 'email', value: '' }];
		showAddress = false;
		editorOpen = true;
	}

	function openEdit() {
		if (!selected) return;
		editing = selected;
		draft = draftOf(selected);
		channels = channelsFromDraft();
		if (channels.length === 0) {
			channels = [{ id: nextChannelId++, kind: 'email', value: '' }];
		}
		showAddress = Boolean(
			selected.homeStreet ||
				selected.homeCity ||
				selected.homePostalCode ||
				selected.businessStreet ||
				selected.businessCity ||
				selected.businessPostalCode
		);
		editorOpen = true;
	}

	async function submit(event: SubmitEvent) {
		event.preventDefault();
		syncChannels();
		if (!draft.fileAs.trim() && !draft.firstName.trim() && !draft.lastName.trim() &&
			!draft.email1.trim() && !draft.company.trim()) {
			toast.push('请至少填写姓名、公司或邮箱', 'error');
			return;
		}
		for (const email of [draft.email1, draft.email2, draft.email3]) {
			const value = email.trim();
			if (value && (!value.includes('@') || /\s/.test(value))) {
				toast.push(`邮箱格式不正确：${value}`, 'error');
				return;
			}
		}
		saving = true;
		try {
			const body: ContactInput = { ...draft };
			const res = editing
				? await mailApi.updateContact(editing.id, body)
				: await mailApi.createContact(body);
			editorOpen = false;
			await load(query.trim());
			selected = contacts.find((c) => c.id === res.contact.id) ?? res.contact;
			toast.ok(editing ? '联系人已更新' : '联系人已创建');
		} catch (err) {
			toast.fail(err, editing ? '更新失败' : '创建失败');
		} finally {
			saving = false;
		}
	}

	async function remove() {
		if (!selected) return;
		if (!window.confirm(`删除联系人“${selected.display}”？`)) return;
		const id = selected.id;
		try {
			await mailApi.deleteContact(id);
			selected = null;
			editing = null;
			await load(query.trim());
			toast.ok('联系人已删除');
		} catch (err) {
			toast.fail(err, '删除失败');
		}
	}

	function emails(contact: Contact): string[] {
		return [contact.email1, contact.email2, contact.email3].filter(Boolean);
	}

	function phones(contact: Contact): Array<{ label: string; value: string }> {
		return [
			{ label: '手机', value: contact.mobilePhone },
			{ label: '工作', value: contact.businessPhone },
			{ label: '家庭', value: contact.homePhone },
			{ label: '家庭 2', value: contact.home2Phone },
			{ label: '传真', value: contact.businessFax }
		].filter((item) => item.value);
	}

	function addressLines(street: string, city: string, state: string, postal: string, country: string) {
		return [street, [state, city].filter(Boolean).join(' '), postal, country].filter(Boolean);
	}

	let homeAddress = $derived.by(() =>
		selected
			? addressLines(
					selected.homeStreet,
					selected.homeCity,
					selected.homeState,
					selected.homePostalCode,
					selected.homeCountry
				)
			: []
	);
	let workAddress = $derived.by(() =>
		selected
			? addressLines(
					selected.businessStreet,
					selected.businessCity,
					selected.businessState,
					selected.businessPostalCode,
					selected.businessCountry
				)
			: []
	);

	function isCJK(value: string): boolean {
		if (!value) return false;
		return [...value].every((char) => char.codePointAt(0)! >= 0x2e80);
	}

	// Mirrors the server's display-name rule so the preview never disagrees
	// with what gets stored.
	let previewName = $derived.by(() => {
		const fileAs = draft.fileAs.trim();
		if (fileAs) return fileAs;
		const first = draft.firstName.trim();
		const last = draft.lastName.trim();
		if (first || last) {
			if (isCJK(first) && isCJK(last)) return last + first;
			return [first, last].filter(Boolean).join(' ');
		}
		const email = channels
			.find((channel) => channel.kind === 'email' && channel.value.trim())
			?.value.trim();
		return email || draft.email1.trim() || '未命名';
	});

	let avatarText = $derived((Array.from(previewName)[0] ?? '?').toUpperCase());
</script>

<svelte:head>
	<title>联系人 · lifemail</title>
</svelte:head>

<div class="lg:grid lg:h-[calc(100dvh-9rem)] lg:grid-cols-[20rem_minmax(0,1fr)] lg:gap-4">
	<!-- Contact list -->
	<section
		class="surface mb-4 flex min-h-[50vh] flex-col overflow-hidden lg:mb-0 lg:min-h-0 {selected
			? 'hidden lg:flex'
			: 'flex'}"
	>
		<header class="shrink-0 space-y-2 border-b border-zinc-200 p-3 dark:border-zinc-800">
			<label class="relative block">
				<span class="sr-only">搜索联系人</span>
				<Icon
					name="search"
					size={15}
					class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-zinc-400"
				/>
				<input
					class="{input} pl-8"
					bind:value={query}
					oninput={onQueryInput}
					placeholder="搜索姓名、公司或邮箱"
					type="search"
				/>
			</label>
			<div class="flex items-center gap-2">
				<p class="muted min-w-0 flex-1 text-[11px]">
					{searching ? '搜索中…' : `${contacts.length} 位联系人`}
				</p>
				<Button variant="primary" size="sm" onclick={openCreate}>
					<Icon name="plus" size={13} />新建
				</Button>
			</div>
		</header>

		<div class="min-h-0 flex-1 overflow-y-auto">
			{#if loading}
				<div class="p-3"><Skeleton rows={6} /></div>
			{:else if contacts.length === 0}
				<EmptyState
					icon="users"
					title={query ? '没有匹配的联系人' : '还没有联系人'}
					description={query ? '换个关键词试试。' : '点击「新建」添加第一位联系人。'}
				/>
			{:else}
				<ul class="divide-y divide-zinc-100 dark:divide-zinc-800">
					{#each contacts as contact (contact.id)}
						<li>
							<button
								type="button"
								class="flex w-full items-center gap-2.5 px-3 py-2.5 text-left transition-colors hover:bg-zinc-50 dark:hover:bg-zinc-800/60 {selected?.id ===
								contact.id
									? 'bg-brand-50 dark:bg-brand-500/15'
									: ''}"
								onclick={() => (selected = contact)}
							>
								<span
									class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-zinc-100 text-xs font-semibold text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300"
								>
									{contact.display.trim().slice(0, 1).toUpperCase() || '?'}
								</span>
								<span class="min-w-0 flex-1">
									<span class="block truncate text-[13px] font-medium">{contact.display}</span>
									<span class="muted block truncate text-[11px]">
										{[contact.company, contact.email1].filter(Boolean).join(' · ') || '—'}
									</span>
								</span>
							</button>
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	</section>

	<!-- Detail -->
	<section
		class="surface flex min-h-[50vh] flex-col overflow-hidden lg:min-h-0 {selected
			? 'flex'
			: 'hidden lg:flex'}"
	>
		{#if !selected}
			<div class="flex flex-1 items-center justify-center">
				<EmptyState icon="users" title="选择一位联系人" description="详细信息会显示在这里。" />
			</div>
		{:else}
			<header
				class="flex shrink-0 items-center gap-2 border-b border-zinc-200 px-4 py-3 dark:border-zinc-800"
			>
				<button
					type="button"
					class="{iconButton} lg:hidden"
					onclick={() => (selected = null)}
					aria-label="返回列表"
				>
					<Icon name="arrowLeft" size={16} />
				</button>
				<span
					class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-brand-600 text-sm font-semibold text-white"
				>
					{selected.display.trim().slice(0, 1).toUpperCase() || '?'}
				</span>
				<div class="min-w-0 flex-1">
					<h2 class="truncate text-sm font-semibold">{selected.display}</h2>
					<p class="muted truncate text-xs">
						{[selected.jobTitle, selected.company].filter(Boolean).join(' · ') || '—'}
					</p>
				</div>
				<Button variant="secondary" size="sm" onclick={openEdit}>
					<Icon name="edit" size={13} />编辑
				</Button>
				<button type="button" class="{iconButton} text-red-500" onclick={remove} aria-label="删除联系人">
					<Icon name="trash" size={16} />
				</button>
			</header>

			<div class="min-h-0 flex-1 overflow-y-auto p-4">
				<div class="space-y-5">
					{#if emails(selected).length > 0}
						<div>
							<p class="muted mb-1.5 text-[11px] font-semibold uppercase tracking-wider">邮箱</p>
							<ul class="space-y-1">
								{#each emails(selected) as address (address)}
									<li>
										<a
											href={`mailto:${address}`}
											class="text-[13px] text-brand-600 hover:underline dark:text-brand-400"
										>
											{address}
										</a>
									</li>
								{/each}
							</ul>
						</div>
					{/if}

					{#if phones(selected).length > 0}
						<div>
							<p class="muted mb-1.5 text-[11px] font-semibold uppercase tracking-wider">电话</p>
							<ul class="space-y-1">
								{#each phones(selected) as phone (phone.label)}
									<li class="flex items-baseline gap-2 text-[13px]">
										<span class="muted w-8 shrink-0 text-[11px]">{phone.label}</span>
										<a
											href={`tel:${phone.value}`}
											class="font-mono text-brand-600 hover:underline dark:text-brand-400"
										>
											{phone.value}
										</a>
									</li>
								{/each}
							</ul>
						</div>
					{/if}

					{#if selected.company || selected.department || selected.jobTitle}
						<div>
							<p class="muted mb-1.5 text-[11px] font-semibold uppercase tracking-wider">单位</p>
							<p class="text-[13px]">
								{[selected.department, selected.jobTitle].filter(Boolean).join(' · ') || '—'}
							</p>
							{#if selected.company}<p class="muted text-[12px]">{selected.company}</p>{/if}
						</div>
					{/if}

					{#if homeAddress.length > 0}
						<div>
							<p class="muted mb-1.5 text-[11px] font-semibold uppercase tracking-wider">家庭地址</p>
							<p class="text-[13px] leading-6">{homeAddress.join('，')}</p>
						</div>
					{/if}

					{#if workAddress.length > 0}
						<div>
							<p class="muted mb-1.5 text-[11px] font-semibold uppercase tracking-wider">单位地址</p>
							<p class="text-[13px] leading-6">{workAddress.join('，')}</p>
						</div>
					{/if}

					{#if selected.body}
						<div>
							<p class="muted mb-1.5 text-[11px] font-semibold uppercase tracking-wider">备注</p>
							<p class="whitespace-pre-wrap text-[13px] leading-6">{selected.body}</p>
						</div>
					{/if}
				</div>
			</div>
		{/if}
	</section>
</div>

<Modal
	open={editorOpen}
	title={editing ? '编辑联系人' : '新建联系人'}
	description="填写常用信息即可，地址可选；保存后同步到 ActiveSync 客户端。"
	onclose={() => (editorOpen = false)}
	width="max-w-3xl"
>
	<div
		class="mb-3 flex items-center gap-3 rounded-xl border border-zinc-200 bg-zinc-50/70 px-3 py-2 dark:border-zinc-800 dark:bg-zinc-800/40"
	>
		<span
			class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-brand-600 text-xs font-semibold text-white"
		>
			{avatarText}
		</span>
		<div class="min-w-0 flex-1">
			<p class="truncate text-[13px] font-medium">{previewName}</p>
			<p class="muted text-[11px]">列表与详情中显示的名称</p>
		</div>
	</div>
	<form id="contact-form" class="max-h-[70vh] space-y-4 overflow-y-auto pr-1" onsubmit={submit}>
		<div>
			<h3 class="mb-2.5 flex items-center gap-2 text-[12px] font-semibold text-zinc-700 dark:text-zinc-200">
				<span class="h-3 w-0.5 rounded-full bg-brand-500"></span>姓名
			</h3>
			<div class="grid grid-cols-2 gap-3 sm:grid-cols-12">
				<Field label="姓" for="contact-last" class="sm:col-span-3">
					<input id="contact-last" class={input} bind:value={draft.lastName} />
				</Field>
				<Field label="名" for="contact-first" class="sm:col-span-3">
					<input id="contact-first" class={input} bind:value={draft.firstName} />
				</Field>
				<Field
					label="显示名"
					for="contact-fileas"
					hint="留空时使用上方预览的名称"
					class="col-span-2 sm:col-span-6"
				>
					<input id="contact-fileas" class={input} bind:value={draft.fileAs} maxlength="100" />
				</Field>
			</div>
		</div>

		<div>
			<h3 class="mb-2.5 flex items-center gap-2 text-[12px] font-semibold text-zinc-700 dark:text-zinc-200">
				<span class="h-3 w-0.5 rounded-full bg-brand-500"></span>单位
			</h3>
			<div class="grid grid-cols-2 gap-3 sm:grid-cols-12">
				<Field label="公司" for="contact-company" class="col-span-2 sm:col-span-6">
					<input id="contact-company" class={input} bind:value={draft.company} />
				</Field>
				<Field label="部门" for="contact-department" class="sm:col-span-3">
					<input id="contact-department" class={input} bind:value={draft.department} />
				</Field>
				<Field label="职务" for="contact-job" class="sm:col-span-3">
					<input id="contact-job" class={input} bind:value={draft.jobTitle} />
				</Field>
			</div>
		</div>

		<div>
			<h3 class="mb-2.5 flex items-center gap-2 text-[12px] font-semibold text-zinc-700 dark:text-zinc-200">
				<span class="h-3 w-0.5 rounded-full bg-brand-500"></span>联系方式
			</h3>
			<div class="space-y-2">
				{#each channels as channel (channel.id)}
					<div class="flex items-start gap-2">
						<select
							class="{input} w-28 shrink-0 sm:w-32"
							bind:value={channel.kind}
							aria-label="联系方式类型"
						>
							{#each CHANNEL_OPTIONS as option (option.kind)}
								<option value={option.kind} disabled={optionDisabled(option.kind, channel)}>
									{option.label}
								</option>
							{/each}
						</select>
						<input
							class={input}
							type={channel.kind === 'email' ? 'email' : 'tel'}
							bind:value={channel.value}
							placeholder={channel.kind === 'email' ? 'name@example.com' : '电话号码'}
							aria-label={CHANNEL_OPTIONS.find((option) => option.kind === channel.kind)?.label}
						/>
						<button
							type="button"
							class="{iconButton} mt-0.5 shrink-0"
							onclick={() => removeChannel(channel.id)}
							aria-label="删除这项联系方式"
						>
							<Icon name="trash" size={15} />
						</button>
					</div>
				{/each}
				<Button variant="ghost" size="sm" onclick={addChannel} disabled={!canAddChannel}>
					<Icon name="plus" size={13} />添加联系方式
				</Button>
			</div>
		</div>

		<div>
			<h3 class="mb-2.5 flex items-center gap-2 text-[12px] font-semibold text-zinc-700 dark:text-zinc-200">
				<span class="h-3 w-0.5 rounded-full bg-brand-500"></span>备注
			</h3>
			<textarea id="contact-body" class={textarea} rows="2" bind:value={draft.body}></textarea>
		</div>

		<div class="rounded-xl border border-zinc-200 dark:border-zinc-800">
			<button
				type="button"
				class="flex w-full items-center gap-2 px-3 py-2.5 text-left transition-colors hover:bg-zinc-50 dark:hover:bg-zinc-800/50"
				onclick={() => (showAddress = !showAddress)}
				aria-expanded={showAddress}
			>
				<Icon name={showAddress ? 'x' : 'plus'} size={14} class="text-zinc-400" />
				<span class="text-[12px] font-medium">
					{showAddress ? '收起地址信息' : '添加地址信息（可选）'}
				</span>
			</button>
			{#if showAddress}
				<div class="space-y-4 border-t border-zinc-200 p-3 dark:border-zinc-800">
					<div>
						<p class="muted mb-2 text-[11px] font-semibold uppercase tracking-wider">家庭地址</p>
						<div class="grid grid-cols-2 gap-3 sm:grid-cols-12">
							<Field label="街道" for="contact-home-street" class="col-span-2 sm:col-span-6">
								<input id="contact-home-street" class={input} bind:value={draft.homeStreet} />
							</Field>
							<Field label="城市" for="contact-home-city" class="sm:col-span-6">
								<input id="contact-home-city" class={input} bind:value={draft.homeCity} />
							</Field>
							<Field label="省 / 州" for="contact-home-state" class="sm:col-span-4">
								<input id="contact-home-state" class={input} bind:value={draft.homeState} />
							</Field>
							<Field label="邮编" for="contact-home-postal" class="sm:col-span-4">
								<input id="contact-home-postal" class={input} bind:value={draft.homePostalCode} />
							</Field>
							<Field label="国家" for="contact-home-country" class="sm:col-span-4">
								<input id="contact-home-country" class={input} bind:value={draft.homeCountry} />
							</Field>
						</div>
					</div>

					<div>
						<p class="muted mb-2 text-[11px] font-semibold uppercase tracking-wider">单位地址</p>
						<div class="grid grid-cols-2 gap-3 sm:grid-cols-12">
							<Field label="街道" for="contact-work-street" class="col-span-2 sm:col-span-6">
								<input id="contact-work-street" class={input} bind:value={draft.businessStreet} />
							</Field>
							<Field label="城市" for="contact-work-city" class="sm:col-span-6">
								<input id="contact-work-city" class={input} bind:value={draft.businessCity} />
							</Field>
							<Field label="省 / 州" for="contact-work-state" class="sm:col-span-4">
								<input id="contact-work-state" class={input} bind:value={draft.businessState} />
							</Field>
							<Field label="邮编" for="contact-work-postal" class="sm:col-span-4">
								<input
									id="contact-work-postal"
									class={input}
									bind:value={draft.businessPostalCode}
								/>
							</Field>
							<Field label="国家" for="contact-work-country" class="sm:col-span-4">
								<input id="contact-work-country" class={input} bind:value={draft.businessCountry} />
							</Field>
						</div>
					</div>
				</div>
			{/if}
		</div>
	</form>

	{#snippet footer()}
		<Button variant="secondary" size="sm" onclick={() => (editorOpen = false)}>取消</Button>
		<Button type="submit" size="sm" form="contact-form" disabled={saving}>
			{editing ? '保存' : '创建'}
		</Button>
	{/snippet}
</Modal>