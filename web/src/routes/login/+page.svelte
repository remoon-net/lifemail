<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	const base = (resolve as (path: string) => string)('/').replace(/\/$/, '');
	import Button from '#lib/components/Button.svelte';
	import Field from '#lib/components/Field.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import { session } from '#lib/session.svelte';
	import { input } from '#lib/ui';

	let token = $state('');
	let busy = $state(false);

	onMount(() => {
		if (session.ready && session.authenticated) {
			void goto(`${base}/`);
		}
	});

	$effect(() => {
		if (session.ready && session.authenticated) {
			void goto(`${base}/`);
		}
	});

	async function submit(event: SubmitEvent) {
		event.preventDefault();
		busy = true;
		try {
			await session.login(token);
			token = '';
			await goto(`${base}/`);
		} catch {
			// session.error carries the message
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>登录 · lifemail</title>
</svelte:head>

<div class="flex min-h-dvh items-center justify-center px-4 py-10">
	<div class="w-full max-w-sm">
		<div class="mb-6 flex items-center gap-3">
			<span
				class="flex h-11 w-11 items-center justify-center rounded-xl bg-brand-600 text-lg font-semibold text-white shadow-lg shadow-brand-600/20"
			>
				L
			</span>
			<div>
				<h1 class="text-lg font-semibold tracking-tight">lifemail</h1>
				<p class="muted text-xs">管理控制台 · {session.version || 'dev'}</p>
			</div>
		</div>

		<form class="surface space-y-4 p-5" onsubmit={submit}>
			<Field label="管理员 Token" for="token" hint="对应 LIFEMAIL_ADMIN_TOKEN">
				<input
					id="token"
					class={input}
					type="password"
					bind:value={token}
					placeholder="••••••••••••"
					autocomplete="current-password"
					required
				/>
			</Field>
			{#if session.error}
				<p class="flex items-center gap-1.5 text-xs text-red-600 dark:text-red-400">
					<Icon name="alert" size={14} />{session.error}
				</p>
			{/if}
			<Button type="submit" disabled={busy} class="w-full">
				{#if busy}
					<Icon name="refresh" size={15} class="animate-spin" />验证中…
				{:else}
					登录
				{/if}
			</Button>
			<p class="muted text-center text-[11px]">会话保存在 HttpOnly Cookie 中，12 小时后失效。</p>
		</form>
	</div>
</div>
