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
		editorOpen = true;
	}

	function openEdit() {
		if (!selected) return;
		editing = selected;
		draft = draftOf(selected);
		editorOpen = true;
	}

	async function submit(event: SubmitEvent) {
		event.preventDefault();
		if (!draft.fileAs.trim() && !draft.firstName.trim() && !draft.lastName.trim() &&
			!draft.email1.trim() && !draft.company.trim()) {
			toast.push('请至少填写姓名、公司或邮箱', 'error');
			return;
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
				<Button variant="secondary" size="sm" onclick={openCreate}>
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
	description="联系人会同步到所有 ActiveSync 客户端。"
	onclose={() => (editorOpen = false)}
	width="max-w-2xl"
>
	<form id="contact-form" class="max-h-[65vh] space-y-5 overflow-y-auto pr-1" onsubmit={submit}>
		<div>
			<p class="muted mb-2 text-[11px] font-semibold uppercase tracking-wider">姓名</p>
			<div class="grid gap-3 sm:grid-cols-3">
				<Field label="姓" for="contact-last">
					<input id="contact-last" class={input} bind:value={draft.lastName} />
				</Field>
				<Field label="名" for="contact-first">
					<input id="contact-first" class={input} bind:value={draft.firstName} />
				</Field>
				<Field label="显示名" for="contact-fileas" hint="留空则按姓名生成">
					<input id="contact-fileas" class={input} bind:value={draft.fileAs} maxlength="100" />
				</Field>
			</div>
		</div>

		<div>
			<p class="muted mb-2 text-[11px] font-semibold uppercase tracking-wider">单位</p>
			<div class="grid gap-3 sm:grid-cols-3">
				<Field label="公司" for="contact-company">
					<input id="contact-company" class={input} bind:value={draft.company} />
				</Field>
				<Field label="部门" for="contact-department">
					<input id="contact-department" class={input} bind:value={draft.department} />
				</Field>
				<Field label="职务" for="contact-job">
					<input id="contact-job" class={input} bind:value={draft.jobTitle} />
				</Field>
			</div>
		</div>

		<div>
			<p class="muted mb-2 text-[11px] font-semibold uppercase tracking-wider">邮箱</p>
			<div class="grid gap-3 sm:grid-cols-3">
				<Field label="邮箱 1" for="contact-email1">
					<input id="contact-email1" class={input} type="email" bind:value={draft.email1} />
				</Field>
				<Field label="邮箱 2" for="contact-email2">
					<input id="contact-email2" class={input} type="email" bind:value={draft.email2} />
				</Field>
				<Field label="邮箱 3" for="contact-email3">
					<input id="contact-email3" class={input} type="email" bind:value={draft.email3} />
				</Field>
			</div>
		</div>

		<div>
			<p class="muted mb-2 text-[11px] font-semibold uppercase tracking-wider">电话</p>
			<div class="grid gap-3 sm:grid-cols-4">
				<Field label="手机" for="contact-mobile">
					<input id="contact-mobile" class={input} type="tel" bind:value={draft.mobilePhone} />
				</Field>
				<Field label="工作" for="contact-business-phone">
					<input id="contact-business-phone" class={input} type="tel" bind:value={draft.businessPhone} />
				</Field>
				<Field label="家庭" for="contact-home-phone">
					<input id="contact-home-phone" class={input} type="tel" bind:value={draft.homePhone} />
				</Field>
				<Field label="传真" for="contact-fax">
					<input id="contact-fax" class={input} type="tel" bind:value={draft.businessFax} />
				</Field>
			</div>
		</div>

		<div>
			<p class="muted mb-2 text-[11px] font-semibold uppercase tracking-wider">家庭地址</p>
			<div class="grid gap-3 sm:grid-cols-2">
				<Field label="街道" for="contact-home-street">
					<input id="contact-home-street" class={input} bind:value={draft.homeStreet} />
				</Field>
				<Field label="城市" for="contact-home-city">
					<input id="contact-home-city" class={input} bind:value={draft.homeCity} />
				</Field>
				<Field label="省 / 州" for="contact-home-state">
					<input id="contact-home-state" class={input} bind:value={draft.homeState} />
				</Field>
				<Field label="邮编" for="contact-home-postal">
					<input id="contact-home-postal" class={input} bind:value={draft.homePostalCode} />
				</Field>
				<Field label="国家" for="contact-home-country">
					<input id="contact-home-country" class={input} bind:value={draft.homeCountry} />
				</Field>
			</div>
		</div>

		<div>
			<p class="muted mb-2 text-[11px] font-semibold uppercase tracking-wider">单位地址</p>
			<div class="grid gap-3 sm:grid-cols-2">
				<Field label="街道" for="contact-work-street">
					<input id="contact-work-street" class={input} bind:value={draft.businessStreet} />
				</Field>
				<Field label="城市" for="contact-work-city">
					<input id="contact-work-city" class={input} bind:value={draft.businessCity} />
				</Field>
				<Field label="省 / 州" for="contact-work-state">
					<input id="contact-work-state" class={input} bind:value={draft.businessState} />
				</Field>
				<Field label="邮编" for="contact-work-postal">
					<input id="contact-work-postal" class={input} bind:value={draft.businessPostalCode} />
				</Field>
				<Field label="国家" for="contact-work-country">
					<input id="contact-work-country" class={input} bind:value={draft.businessCountry} />
				</Field>
			</div>
		</div>

		<Field label="备注" for="contact-body">
			<textarea id="contact-body" class={textarea} rows="3" bind:value={draft.body}></textarea>
		</Field>
	</form>

	{#snippet footer()}
		<Button variant="secondary" size="sm" onclick={() => (editorOpen = false)}>取消</Button>
		<Button type="submit" size="sm" form="contact-form" disabled={saving}>
			{editing ? '保存' : '创建'}
		</Button>
	{/snippet}
</Modal>
