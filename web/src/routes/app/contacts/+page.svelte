<script lang="ts">
	import { onMount } from 'svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import { mailApi } from '#lib/mail-api';
	import { toast } from '#lib/toast.svelte';
	import { iconButton, input } from '#lib/ui';
	import type { Contact } from '#lib/mail-types';

	let contacts = $state<Contact[]>([]);
	let query = $state('');
	let selected = $state<Contact | null>(null);
	let loading = $state(true);
	let searching = $state(false);
	let timer: ReturnType<typeof setTimeout> | undefined;

	async function load(term: string) {
		searching = true;
		try {
			const res = await mailApi.contacts(term);
			contacts = res.contacts;
			if (selected && !contacts.some((c) => c.id === selected?.id)) {
				selected = null;
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
		<header class="shrink-0 border-b border-zinc-200 p-3 dark:border-zinc-800">
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
			<p class="muted mt-2 text-[11px]">
				{searching ? '搜索中…' : `${contacts.length} 位联系人`}
			</p>
		</header>

		<div class="min-h-0 flex-1 overflow-y-auto">
			{#if loading}
				<div class="p-3"><Skeleton rows={6} /></div>
			{:else if contacts.length === 0}
				<EmptyState
					icon="users"
					title={query ? '没有匹配的联系人' : '还没有联系人'}
					description={query ? '换个关键词试试。' : '联系人由 ActiveSync 客户端写入。'}
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
							<p class="text-[13px]">{[selected.department, selected.jobTitle].filter(Boolean).join(' · ') || '—'}</p>
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
