<script lang="ts">
	import { onMount } from 'svelte';
	import Badge from '#lib/components/Badge.svelte';
	import Button from '#lib/components/Button.svelte';
	import Card from '#lib/components/Card.svelte';
	import CopyButton from '#lib/components/CopyButton.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import Field from '#lib/components/Field.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import Modal from '#lib/components/Modal.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import { api } from '#lib/api';
	import { hueFor, initials, shortDate } from '#lib/format';
	import { toast } from '#lib/toast.svelte';
	import type { Alias, User } from '#lib/types';
	import { iconButton, iconButtonDanger, input } from '#lib/ui';

	let users = $state<User[]>([]);
	let aliases = $state<Alias[]>([]);
	let loading = $state(true);
	let search = $state('');

	let createOpen = $state(false);
	let createForm = $state({ name: '', address: '', display_name: '' });
	let createBusy = $state(false);

	let editing = $state<User | null>(null);
	let editName = $state('');
	let aliasFor = $state<User | null>(null);
	let aliasDraft = $state('');
	let aliasBusy = $state(false);
	let deleting = $state<User | null>(null);
	let resetting = $state<User | null>(null);
	let secret = $state<{ login: string; password: string } | null>(null);

	async function load() {
		loading = true;
		try {
			const [u, a] = await Promise.all([api.users(), api.aliases()]);
			users = [...u.users].sort((a, b) => a.name.localeCompare(b.name));
			aliases = [...a.aliases].sort((a, b) => a.address.localeCompare(b.address));
		} catch (err) {
			toast.fail(err, '无法读取用户');
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void load();
	});

	let filtered = $derived(
		users.filter((user) => {
			const term = search.trim().toLowerCase();
			if (!term) return true;
			const owned = aliases
				.filter((a) => a.user_id === user.id)
				.map((a) => a.address)
				.join(' ');
			return `${user.name} ${user.display_name} ${user.address} ${owned}`.toLowerCase().includes(term);
		})
	);

	function addressesOf(userID: string): Alias[] {
		return aliases.filter((a) => a.user_id === userID);
	}

	async function createUser(event: SubmitEvent) {
		event.preventDefault();
		createBusy = true;
		try {
			const result = await api.createUser({
				name: createForm.name.trim(),
				address: createForm.address.trim(),
				display_name: createForm.display_name.trim()
			});
			createOpen = false;
			createForm = { name: '', address: '', display_name: '' };
			await load();
			if (result.password) secret = { login: result.user.name, password: result.password };
			toast.ok(`已创建 ${result.user.name}`);
		} catch (err) {
			toast.fail(err, '创建失败');
		} finally {
			createBusy = false;
		}
	}

	function openEdit(user: User) {
		editing = user;
		editName = user.display_name;
	}

	async function saveEdit() {
		if (!editing) return;
		try {
			await api.updateUser(editing.id, { display_name: editName.trim() });
			editing = null;
			await load();
			toast.ok('显示名已更新');
		} catch (err) {
			toast.fail(err, '保存失败');
		}
	}

	async function resetPassword(user: User) {
		resetting = null;
		try {
			const result = await api.updateUser(user.id, { password: '' });
			await load();
			if (result.password) secret = { login: result.user.name, password: result.password };
			toast.ok(`已重置 ${user.name} 的密码`);
		} catch (err) {
			toast.fail(err, '重置失败');
		}
	}

	async function removeUser() {
		if (!deleting) return;
		const user = deleting;
		deleting = null;
		try {
			await api.deleteUser(user.id);
			await load();
			toast.ok(`已删除 ${user.name}`);
		} catch (err) {
			toast.fail(err, '删除失败');
		}
	}

	async function addAlias(event: SubmitEvent) {
		event.preventDefault();
		if (!aliasFor) return;
		aliasBusy = true;
		try {
			await api.createAlias({ address: aliasDraft.trim(), user_id: aliasFor.id });
			toast.ok(`已添加 ${aliasDraft.trim()}`);
			aliasDraft = '';
			aliasFor = null;
			await load();
		} catch (err) {
			toast.fail(err, '添加别名失败');
		} finally {
			aliasBusy = false;
		}
	}

	async function removeAlias(alias: Alias) {
		try {
			await api.deleteAlias(alias.address);
			await load();
			toast.ok(`已删除 ${alias.address}`);
		} catch (err) {
			toast.fail(err, '删除别名失败');
		}
	}

	async function makePrimary(user: User, alias: Alias) {
		if (alias.is_primary) return;
		try {
			await api.updateUser(user.id, { address: alias.address });
			await load();
			toast.ok(`${user.name} 的主地址已改为 ${alias.address}`);
		} catch (err) {
			toast.fail(err, '修改主地址失败');
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
			<input class="{input} pl-9" placeholder="搜索账号、地址或别名" bind:value={search} />
		</div>
		<p class="muted ml-auto text-xs">{filtered.length} / {users.length} 个账号</p>
		<Button size="sm" onclick={() => (createOpen = true)}>
			<Icon name="plus" size={14} />新建
		</Button>
	</div>

	<Card padding="none">
		{#if loading}
			<div class="p-4"><Skeleton rows={4} /></div>
		{:else if filtered.length === 0}
			<EmptyState
				icon="users"
				title={search ? '没有匹配的账号' : '还没有账号'}
				description={search ? '换个关键词，或清除搜索条件。' : '创建第一个账号后，EAS / IMAP / SMTP 客户端即可登录。'}
			>
				{#snippet action()}
					{#if !search}
						<Button size="sm" onclick={() => (createOpen = true)}>新建账号</Button>
					{/if}
				{/snippet}
			</EmptyState>
		{:else}
			<ul class="divide-y divide-zinc-100 dark:divide-zinc-800">
				{#each filtered as user (user.id)}
					<li class="flex flex-col gap-3 px-4 py-3.5 sm:flex-row sm:items-start sm:gap-4">
						<span
							class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-[13px] font-semibold text-white"
							style={`background:hsl(${hueFor(user.name)} 55% 45%)`}
							aria-hidden="true">{initials(user.display_name || user.name)}</span
						>

						<div class="min-w-0 flex-1">
							<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
								<span class="text-sm font-medium">{user.name}</span>
								{#if user.display_name && user.display_name.toLowerCase() !== user.name}
									<span class="muted text-xs">{user.display_name}</span>
								{/if}
								{#if user.created_at}
									<span class="muted text-[11px]">创建于 {shortDate(user.created_at)}</span>
								{/if}
							</div>

							<div class="mt-1.5 flex flex-wrap items-center gap-1.5">
								{#each addressesOf(user.id) as alias (alias.address)}
									<span
										class="group inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[11px] {alias.is_primary
											? 'border-brand-200 bg-brand-50 text-brand-700 dark:border-brand-500/30 dark:bg-brand-500/10 dark:text-brand-300'
											: 'border-zinc-200 bg-zinc-50 text-zinc-600 dark:border-zinc-700 dark:bg-zinc-800/60 dark:text-zinc-300'}"
									>
										{alias.address}
										{#if alias.is_primary}
											<span class="text-[10px] opacity-70">主地址</span>
										{:else}
											<button
												type="button"
												class="opacity-0 transition-opacity group-hover:opacity-100 focus:opacity-100"
												title="设为主地址"
												aria-label={`将 ${alias.address} 设为 ${user.name} 的主地址`}
												onclick={() => makePrimary(user, alias)}
											>
												<Icon name="star" size={11} />
											</button>
											<button
												type="button"
												class="opacity-0 transition-opacity group-hover:opacity-100 focus:opacity-100"
												title="删除地址"
												aria-label={`删除 ${alias.address}`}
												onclick={() => removeAlias(alias)}
											>
												<Icon name="x" size={11} />
											</button>
										{/if}
									</span>
								{/each}
								<button
									type="button"
									class="inline-flex items-center gap-0.5 rounded-md border border-dashed border-zinc-300 px-1.5 py-0.5 text-[11px] text-zinc-500 hover:border-brand-400 hover:text-brand-600 dark:border-zinc-600 dark:text-zinc-400"
									onclick={() => {
										aliasFor = user;
										aliasDraft = '';
									}}
								>
									<Icon name="plus" size={11} />地址
								</button>
							</div>
						</div>

						<div class="flex shrink-0 items-center gap-0.5 self-end sm:self-start">
							<button
								type="button"
								class={iconButton}
								title="编辑显示名"
								aria-label={`编辑 ${user.name}`}
								onclick={() => openEdit(user)}
							>
								<Icon name="edit" size={15} />
							</button>
							<button
								type="button"
								class={iconButton}
								title="重置密码"
								aria-label={`重置 ${user.name} 的密码`}
								onclick={() => (resetting = user)}
							>
								<Icon name="key" size={15} />
							</button>
							<button
								type="button"
								class={iconButtonDanger}
								title="删除账号"
								aria-label={`删除 ${user.name}`}
								onclick={() => (deleting = user)}
							>
								<Icon name="trash" size={15} />
							</button>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</Card>
</div>

<Modal open={createOpen} title="新建账号" description="账号名是登录名，不带域名" onclose={() => (createOpen = false)}>
	<form id="create-user" class="space-y-3" onsubmit={createUser}>
		<Field label="账号名" for="new-name" hint="例如 alice，用于 EAS / IMAP 登录">
			<input id="new-name" class={input} bind:value={createForm.name} autocomplete="off" required />
		</Field>
		<Field label="主地址" for="new-address" hint="可选；创建后仍可调整">
			<input
				id="new-address"
				class={input}
				type="email"
				bind:value={createForm.address}
				placeholder="alice@example.com"
				autocomplete="off"
			/>
		</Field>
		<Field label="显示名" for="new-display">
			<input id="new-display" class={input} bind:value={createForm.display_name} autocomplete="off" />
		</Field>
		<p class="muted text-xs">密码由服务端随机生成，只显示一次。</p>
	</form>
	{#snippet footer()}
		<Button variant="secondary" onclick={() => (createOpen = false)}>取消</Button>
		<Button type="submit" form="create-user" disabled={createBusy}>
			{createBusy ? '创建中…' : '创建'}
		</Button>
	{/snippet}
</Modal>

<Modal open={editing !== null} title="编辑显示名" description={editing?.name} onclose={() => (editing = null)}>
	<Field label="显示名" for="edit-display">
		<input id="edit-display" class={input} bind:value={editName} autocomplete="off" />
	</Field>
	{#snippet footer()}
		<Button variant="secondary" onclick={() => (editing = null)}>取消</Button>
		<Button onclick={saveEdit}>保存</Button>
	{/snippet}
</Modal>

<Modal
	open={aliasFor !== null}
	title="添加收信地址"
	description={aliasFor ? `投递到 ${aliasFor.name} 的收件箱` : ''}
	onclose={() => (aliasFor = null)}
>
	<form id="add-alias" onsubmit={addAlias}>
		<Field label="地址" for="alias-address" hint="地址所在域应在「域名与证书」中已配置">
			<input
				id="alias-address"
				class={input}
				type="email"
				bind:value={aliasDraft}
				placeholder="info@example.com"
				autocomplete="off"
				required
			/>
		</Field>
	</form>
	{#snippet footer()}
		<Button variant="secondary" onclick={() => (aliasFor = null)}>取消</Button>
		<Button type="submit" form="add-alias" disabled={aliasBusy}>添加</Button>
	{/snippet}
</Modal>

<Modal
	open={secret !== null}
	title="新密码"
	description="只显示一次，关闭后无法再次查看"
	onclose={() => (secret = null)}
>
	{#if secret}
		<div class="space-y-3">
			<div>
				<p class="muted text-xs">账号</p>
				<p class="text-sm font-medium">{secret.login}</p>
			</div>
			<div class="rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2.5 dark:border-zinc-700 dark:bg-zinc-950">
				<code class="block break-all font-mono text-sm select-all">{secret.password}</code>
			</div>
			<div class="flex items-center gap-2">
				<CopyButton value={secret.password} label="复制密码" />
				<span class="muted text-[11px]">请立即保存到密码管理器</span>
			</div>
		</div>
	{/if}
	{#snippet footer()}
		<Button onclick={() => (secret = null)}>我已保存</Button>
	{/snippet}
</Modal>

<Modal open={resetting !== null} title="重置密码" description={resetting ? `为 ${resetting.name} 生成新密码？旧密码将立即失效。` : ''} onclose={() => (resetting = null)}>
	{#snippet footer()}
		<Button variant="secondary" onclick={() => (resetting = null)}>取消</Button>
		<Button onclick={() => resetting && resetPassword(resetting)}>重置</Button>
	{/snippet}
</Modal>

<Modal
	open={deleting !== null}
	title="删除账号"
	description={deleting ? `删除 ${deleting.name}？邮件、日历、联系人、别名与同步状态都会一并删除，且不可恢复。` : ''}
	onclose={() => (deleting = null)}
>
	{#snippet footer()}
		<Button variant="secondary" onclick={() => (deleting = null)}>取消</Button>
		<Button variant="danger" onclick={removeUser}>删除</Button>
	{/snippet}
</Modal>
