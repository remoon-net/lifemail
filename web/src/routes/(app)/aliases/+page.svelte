<script lang="ts">
	import { onMount } from 'svelte';
	import Badge from '$lib/components/Badge.svelte';
	import Button from '$lib/components/Button.svelte';
	import Card from '$lib/components/Card.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import Field from '$lib/components/Field.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import Skeleton from '$lib/components/Skeleton.svelte';
	import { api } from '$lib/api';
	import { shortDate } from '$lib/format';
	import { toast } from '$lib/toast.svelte';
	import type { Alias, User } from '$lib/types';
	import { iconButtonDanger, input } from '$lib/ui';

	let aliases = $state<Alias[]>([]);
	let users = $state<User[]>([]);
	let loading = $state(true);
	let search = $state('');
	let addOpen = $state(false);
	let draft = $state({ address: '', user_id: '' });
	let deleting = $state<Alias | null>(null);

	async function load() {
		loading = true;
		try {
			const [a, u] = await Promise.all([api.aliases(), api.users()]);
			aliases = [...a.aliases].sort((x, y) => x.address.localeCompare(y.address));
			users = [...u.users].sort((x, y) => x.name.localeCompare(y.name));
		} catch (err) {
			toast.fail(err, '无法读取地址');
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void load();
	});

	function ownerName(userID: string): string {
		return users.find((u) => u.id === userID)?.name ?? userID.slice(0, 8);
	}

	let filtered = $derived(
		aliases.filter((a) => {
			const term = search.trim().toLowerCase();
			if (!term) return true;
			return `${a.address} ${ownerName(a.user_id)}`.toLowerCase().includes(term);
		})
	);

	async function add(event: SubmitEvent) {
		event.preventDefault();
		try {
			await api.createAlias({ address: draft.address.trim(), user_id: draft.user_id });
			addOpen = false;
			draft = { address: '', user_id: '' };
			await load();
			toast.ok('地址已添加');
		} catch (err) {
			toast.fail(err, '添加失败');
		}
	}

	async function remove() {
		if (!deleting) return;
		const target = deleting;
		deleting = null;
		try {
			await api.deleteAlias(target.address);
			await load();
			toast.ok(`已删除 ${target.address}`);
		} catch (err) {
			toast.fail(err, '删除失败');
		}
	}
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-center gap-2">
		<div class="relative min-w-0 flex-1 sm:max-w-xs">
			<Icon
				name="search"
				size={15}
				class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-zinc-400"
			/>
			<input class="{input} pl-9" placeholder="搜索地址或账号" bind:value={search} />
		</div>
		<p class="muted ml-auto text-xs">{filtered.length} 个地址</p>
		<Button size="sm" onclick={() => (addOpen = true)}>
			<Icon name="plus" size={14} />添加地址
		</Button>
	</div>

	<Card padding="none">
		{#if loading}
			<div class="p-4"><Skeleton rows={5} /></div>
		{:else if filtered.length === 0}
			<EmptyState icon="star" title="还没有地址" description="别名把额外地址投递到已有账号，不需要新建账号。">
				{#snippet action()}
					<Button size="sm" onclick={() => (addOpen = true)}>添加地址</Button>
				{/snippet}
			</EmptyState>
		{:else}
			<ul class="divide-y divide-zinc-100 dark:divide-zinc-800">
				{#each filtered as alias (alias.address)}
					<li class="flex items-center gap-3 px-4 py-3">
						<div class="min-w-0 flex-1">
							<p class="truncate font-mono text-[13px]">{alias.address}</p>
							<p class="muted mt-0.5 text-xs">
								→ {ownerName(alias.user_id)}
								{#if alias.created_at}<span class="opacity-70">· {shortDate(alias.created_at)}</span>{/if}
							</p>
						</div>
						{#if alias.is_primary}
							<Badge tone="brand">主地址</Badge>
						{:else}
							<button
								type="button"
								class={iconButtonDanger}
								title="删除地址"
								aria-label={`删除 ${alias.address}`}
								onclick={() => (deleting = alias)}
							>
								<Icon name="trash" size={15} />
							</button>
						{/if}
					</li>
				{/each}
			</ul>
		{/if}
	</Card>
</div>

<Modal open={addOpen} title="添加收信地址" description="共享地址直接投递到某个账号的收件箱" onclose={() => (addOpen = false)}>
	<form id="alias-form" class="space-y-3" onsubmit={add}>
		<Field label="地址" for="alias-address">
			<input
				id="alias-address"
				class={input}
				type="email"
				bind:value={draft.address}
				placeholder="info@example.com"
				autocomplete="off"
				required
			/>
		</Field>
		<Field label="投递到" for="alias-owner">
			<select id="alias-owner" class={input} bind:value={draft.user_id} required>
				<option value="" disabled>选择账号</option>
				{#each users as user (user.id)}
					<option value={user.id}>{user.name}{user.display_name ? ` (${user.display_name})` : ''}</option>
				{/each}
			</select>
		</Field>
	</form>
	{#snippet footer()}
		<Button variant="secondary" onclick={() => (addOpen = false)}>取消</Button>
		<Button type="submit" form="alias-form">添加</Button>
	{/snippet}
</Modal>

<Modal open={deleting !== null} title="删除地址" description={deleting ? `删除 ${deleting.address}？该地址收到的邮件将不再投递。` : ''} onclose={() => (deleting = null)}>
	{#snippet footer()}
		<Button variant="secondary" onclick={() => (deleting = null)}>取消</Button>
		<Button variant="danger" onclick={remove}>删除</Button>
	{/snippet}
</Modal>
