<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	const appBase = (resolve as (path: string) => string)('/app');
	import Button from '#lib/components/Button.svelte';
	import Field from '#lib/components/Field.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import { userSession } from '#lib/user-session.svelte';
	import { input } from '#lib/ui';

	let login = $state('');
	let password = $state('');
	let busy = $state(false);

	onMount(() => {
		if (userSession.ready && userSession.authenticated) {
			void goto(`${appBase}/`);
		}
	});

	$effect(() => {
		if (userSession.ready && userSession.authenticated) {
			void goto(`${appBase}/`);
		}
	});

	async function submit(event: SubmitEvent) {
		event.preventDefault();
		busy = true;
		try {
			await userSession.login(login.trim(), password);
			password = '';
			await goto(`${appBase}/`);
		} catch {
			// userSession.error carries the message
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>登录邮箱 · lifemail</title>
</svelte:head>

<div class="relative flex min-h-dvh items-center justify-center overflow-hidden px-4 py-10">
	<div
		class="pointer-events-none absolute inset-x-0 -top-40 h-80 bg-gradient-to-b from-brand-200/60 to-transparent blur-3xl dark:from-brand-500/15"
	></div>
	<div class="relative w-full max-w-sm">
		<div class="mb-6 flex items-center gap-3">
			<span
				class="flex h-11 w-11 items-center justify-center rounded-xl bg-brand-600 text-lg font-semibold text-white shadow-lg shadow-brand-600/25"
			>
				L
			</span>
			<div>
				<h1 class="text-lg font-semibold tracking-tight">lifemail</h1>
				<p class="muted text-xs">邮箱登录 · {userSession.version || 'dev'}</p>
			</div>
		</div>

		<form class="surface space-y-4 p-5" onsubmit={submit}>
			<Field label="账号或邮箱地址" for="login">
				<input
					id="login"
					class={input}
					type="text"
					bind:value={login}
					placeholder="name@example.com"
					autocomplete="username"
					required
				/>
			</Field>
			<Field label="密码" for="password">
				<input
					id="password"
					class={input}
					type="password"
					bind:value={password}
					placeholder="••••••••"
					autocomplete="current-password"
					required
				/>
			</Field>
			{#if userSession.error}
				<p class="flex items-center gap-1.5 text-xs text-red-600 dark:text-red-400">
					<Icon name="alert" size={14} />{userSession.error}
				</p>
			{/if}
			<Button type="submit" disabled={busy} class="w-full">
				{#if busy}
					<Icon name="refresh" size={15} class="animate-spin" />登录中…
				{:else}
					登录
				{/if}
			</Button>
			<p class="muted text-center text-[11px]">
				与 IMAP / SMTP 使用同一套账号密码，登录状态保留 12 小时。
			</p>
		</form>
	</div>
</div>
