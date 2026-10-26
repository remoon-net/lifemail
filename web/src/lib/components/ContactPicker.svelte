<script lang="ts">
	import Icon from './Icon.svelte';
	import { initials } from '#lib/format';
	import type { Contact } from '#lib/mail-types';

	type Props = {
		/** Every contact of the account; filtering happens in the browser. */
		contacts: Contact[];
		value: string[];
		onchange: (next: string[]) => void;
		placeholder?: string;
		loading?: boolean;
		id?: string;
		/** Called with the typed address so the parent can add it to the address book. */
		onadd?: (seed: { name: string; email: string }) => void;
	};

	let {
		contacts,
		value,
		onchange,
		placeholder = '搜索联系人或输入邮箱',
		loading = false,
		id,
		onadd
	}: Props = $props();

	// Deliberately permissive: the server only requires an "@" and no spaces.
	const EMAIL = /[^\s<>@,;:]+@[^\s<>@,;:]+\.[^\s<>@,;:]+/;

	type Option = { key: string; name: string; address: string; company: string };

	// One option per address: a contact may hold up to three emails.
	const options = $derived.by(() => {
		const list: Option[] = [];
		for (const contact of contacts) {
			const name = contact.display || contact.fileAs;
			for (const raw of [contact.email1, contact.email2, contact.email3]) {
				const address = raw.trim();
				if (!address) continue;
				list.push({
					key: `${contact.id}:${address}`,
					name,
					address,
					company: contact.company
				});
			}
		}
		return list;
	});

	const nameByAddress = $derived.by(() => {
		const map = new Map<string, string>();
		for (const option of options) {
			const key = option.address.toLowerCase();
			if (option.name && !map.has(key)) map.set(key, option.name);
		}
		return map;
	});

	let query = $state('');
	let open = $state(false);
	let highlighted = $state(0);
	let wrapper = $state<HTMLDivElement | null>(null);
	let inputEl = $state<HTMLInputElement | null>(null);

	const picked = $derived(new Set(value.map((address) => address.toLowerCase())));
	const term = $derived(query.trim().toLowerCase());

	const matches = $derived.by(() => {
		const pool = options.filter((option) => !picked.has(option.address.toLowerCase()));
		if (!term) return pool.slice(0, 8);
		return pool
			.filter((option) =>
				`${option.name} ${option.address} ${option.company}`.toLowerCase().includes(term)
			)
			.slice(0, 8);
	});

	const typedEmail = $derived.by(() => {
		const hit = EMAIL.exec(query);
		return hit ? hit[0].toLowerCase() : '';
	});

	const typedName = $derived.by(() => {
		if (!typedEmail) return '';
		return query
			.replace(EMAIL, '')
			.replace(/[<>",;]/g, ' ')
			.replace(/\s+/g, ' ')
			.trim();
	});

	// A full address that is not in the address book can be added from here.
	const canAdd = $derived(
		Boolean(
			onadd &&
				typedEmail &&
				!picked.has(typedEmail) &&
				!options.some((option) => option.address.toLowerCase() === typedEmail)
		)
	);

	const rows = $derived(matches.length + (canAdd ? 1 : 0));

	$effect(() => {
		void term;
		highlighted = 0;
	});

	// Close on a click outside, including clicks on the surrounding dialog.
	$effect(() => {
		if (!open) return;
		function onPointerDown(event: PointerEvent) {
			if (wrapper && !wrapper.contains(event.target as Node)) open = false;
		}
		document.addEventListener('pointerdown', onPointerDown, true);
		return () => document.removeEventListener('pointerdown', onPointerDown, true);
	});

	function choose(option: Option) {
		if (picked.has(option.address.toLowerCase())) return;
		onchange([...value, option.address]);
		query = '';
		open = true;
		inputEl?.focus();
	}

	function addContact() {
		if (!canAdd || !onadd) return;
		onadd({ name: typedName || (typedEmail.split('@')[0] ?? ''), email: typedEmail });
		query = '';
		open = false;
	}

	function remove(address: string) {
		onchange(value.filter((item) => item !== address));
		inputEl?.focus();
	}

	function onkeydown(event: KeyboardEvent) {
		if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
			if (!open) {
				open = true;
				return;
			}
			if (rows === 0) return;
			event.preventDefault();
			const step = event.key === 'ArrowDown' ? 1 : -1;
			highlighted = (highlighted + step + rows) % rows;
			return;
		}
		if (event.key === 'Escape') {
			if (open) {
				event.preventDefault();
				event.stopPropagation();
				open = false;
			}
			query = '';
			return;
		}
		if (event.key === 'Enter') {
			event.preventDefault();
			if (!open) {
				open = true;
				return;
			}
			const option = matches[highlighted];
			if (option) choose(option);
			else if (canAdd) addContact();
			return;
		}
		if (event.key === 'Backspace' && query === '' && value.length > 0) {
			event.preventDefault();
			const last = value[value.length - 1];
			if (last) remove(last);
		}
	}
</script>

<div bind:this={wrapper}>
	<label
		class="flex min-h-9.5 w-full flex-wrap items-center gap-1.5 rounded-lg border border-zinc-300 bg-white px-2 py-1.5 transition-colors focus-within:border-brand-500 focus-within:ring-2 focus-within:ring-brand-500/20 dark:border-zinc-700 dark:bg-zinc-950"
	>
		{#each value as address (address)}
			{@const name = nameByAddress.get(address.toLowerCase()) ?? ''}
			<span
				class="inline-flex max-w-full items-center gap-1 rounded-md bg-brand-50 py-0.5 pr-1 pl-1.5 text-[12px] font-medium text-brand-800 dark:bg-brand-500/15 dark:text-brand-200"
				title={name ? `${name} <${address}>` : address}
			>
				<span class="max-w-40 truncate">{name || address}</span>
				{#if name}
					<span class="max-w-32 truncate text-[10px] font-normal opacity-60">{address}</span>
				{/if}
				<button
					type="button"
					class="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded transition-colors hover:bg-brand-200/60 dark:hover:bg-brand-500/25"
					onclick={(event) => {
						event.stopPropagation();
						remove(address);
					}}
					aria-label={`移除 ${address}`}
				>
					<Icon name="x" size={10} stroke={2.4} />
				</button>
			</span>
		{/each}
		<input
			{id}
			bind:this={inputEl}
			bind:value={query}
			class="h-6 min-w-28 flex-1 bg-transparent px-0.5 text-sm text-zinc-900 placeholder:text-zinc-400 focus:outline-none dark:text-zinc-100 dark:placeholder:text-zinc-500"
			{placeholder}
			autocomplete="off"
			spellcheck="false"
			onfocus={() => (open = true)}
			oninput={() => (open = true)}
			{onkeydown}
		/>
	</label>

	{#if open}
		<div
			class="mt-1 overflow-hidden rounded-xl border border-zinc-200 bg-white py-1 shadow-sm dark:border-zinc-700 dark:bg-zinc-900"
		>
			{#if matches.length > 0}
				<ul class="max-h-56 overflow-y-auto overscroll-contain">
					{#each matches as option, index (option.key)}
						<li>
							<button
								type="button"
								class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left transition-colors {index ===
								highlighted
									? 'bg-brand-50 dark:bg-brand-500/10'
									: 'hover:bg-zinc-50 dark:hover:bg-zinc-800/60'}"
								onmousedown={(event) => event.preventDefault()}
								onclick={() => choose(option)}
							>
								<span
									class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-zinc-100 text-[11px] font-semibold text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300"
								>
									{initials(option.name || option.address)}
								</span>
								<span class="min-w-0 flex-1">
									<span class="block truncate text-[12.5px] font-medium">
										{option.name || option.address}
									</span>
									<span class="muted block truncate text-[11px]">
										{option.address}{option.company ? ` · ${option.company}` : ''}
									</span>
								</span>
								{#if index === highlighted}
									<Icon name="arrowRight" size={12} class="shrink-0 opacity-40" />
								{/if}
							</button>
						</li>
					{/each}
				</ul>
			{:else if loading}
				<p class="muted px-3 py-2 text-[12px]">正在加载联系人…</p>
			{:else if !canAdd}
				<p class="muted px-3 py-2 text-[12px]">
					{term ? '没有匹配的联系人，输入完整邮箱可以新建。' : '通讯录里还没有可用的邮箱。'}
				</p>
			{/if}

			{#if canAdd}
				<button
					type="button"
					class="flex w-full items-center gap-2 border-t border-zinc-100 px-2.5 py-2 text-left transition-colors dark:border-zinc-800 {matches.length >
					0
						? 'hover:bg-amber-50 dark:hover:bg-amber-500/10'
						: 'hover:bg-zinc-50 dark:hover:bg-zinc-800/60'}"
					onmousedown={(event) => event.preventDefault()}
					onclick={addContact}
				>
					<span
						class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300"
					>
						<Icon name="plus" size={13} stroke={2.2} />
					</span>
					<span class="min-w-0 flex-1">
						<span class="block text-[12.5px] font-medium">
							添加「{typedName || typedEmail}」为新联系人
						</span>
						<span class="muted block truncate text-[11px]">{typedEmail}</span>
					</span>
				</button>
			{/if}
		</div>
	{/if}
</div>
