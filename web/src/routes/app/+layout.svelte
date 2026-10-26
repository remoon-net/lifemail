<script lang="ts">
	import { onMount, type Snippet } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	const base = (resolve as (path: string) => string)('/app');
	import { page } from '$app/state';
	import Icon from '#lib/components/Icon.svelte';
	import Skeleton from '#lib/components/Skeleton.svelte';
	import { mailEvents } from '#lib/mail-events.svelte';
	import { userSession } from '#lib/user-session.svelte';
	import { theme } from '#lib/theme.svelte';
	import { iconButton } from '#lib/ui';
	import type { IconName } from '#lib/icons';

	let { children }: { children: Snippet } = $props();
	let mobileOpen = $state(false);

	const nav: Array<{ href: string; label: string; icon: IconName }> = [
		{ href: '/', label: '邮件', icon: 'mail' },
		{ href: '/calendar/', label: '日历', icon: 'calendar' },
		{ href: '/contacts/', label: '联系人', icon: 'users' }
	];

	const titles: Record<string, string> = {
		'/': '邮件',
		'/calendar': '日历',
		'/contacts': '联系人'
	};

	function hrefFor(path: string): string {
		return path === '/' ? `${base}/` : `${base}${path}`;
	}

	function isActive(path: string): boolean {
		const current = page.url.pathname;
		if (path === '/') return current === `${base}/` || current === base;
		return current.startsWith(hrefFor(path));
	}

	const relative = $derived(page.url.pathname.slice(base.length).replace(/\/+$/, ''));
	let isLogin = $derived(relative === '/login');
	let title = $derived(titles[relative === '' ? '/' : relative] ?? '邮箱');

	onMount(() => {
		void userSession.boot();
		const onUnauthorized = () => {
			userSession.markUnauthenticated();
			void goto(`${base}/login/`);
		};
		window.addEventListener('lifemail:user-unauthorized', onUnauthorized);
		return () => window.removeEventListener('lifemail:user-unauthorized', onUnauthorized);
	});

	$effect(() => {
		if (userSession.ready && !userSession.authenticated && !isLogin) {
			void goto(`${base}/login/`);
		}
	});

	$effect(() => {
		// One event stream per tab for the whole signed-in session; signing out
		// (or losing the cookie) closes it.
		if (!userSession.authenticated) return;
		mailEvents.start();
		return () => mailEvents.stop();
	});

	$effect(() => {
		// Close the drawer after navigating on mobile.
		void page.url.pathname;
		mobileOpen = false;
	});

	async function logout() {
		await userSession.logout();
		await goto(`${base}/login/`);
	}
</script>

<svelte:head>
	<title>{isLogin ? '登录邮箱' : title} · lifemail</title>
</svelte:head>

{#if isLogin}
	{@render children()}
{:else if !userSession.ready}
	<div class="mx-auto max-w-5xl px-6 py-16">
		<Skeleton rows={6} />
	</div>
{:else if userSession.authenticated}
	<div class="min-h-dvh lg:grid lg:grid-cols-[14rem_1fr]">
		{#if mobileOpen}
			<button
				type="button"
				class="fixed inset-0 z-30 bg-zinc-950/40 backdrop-blur-xs lg:hidden"
				onclick={() => (mobileOpen = false)}
				aria-label="关闭菜单"
			></button>
		{/if}

		<aside
			class="fixed inset-y-0 left-0 z-40 flex w-60 flex-col border-r border-zinc-200 bg-white transition-transform dark:border-zinc-800 dark:bg-zinc-900 lg:sticky lg:top-0 lg:z-auto lg:h-dvh lg:w-auto lg:translate-x-0 {mobileOpen
				? 'translate-x-0'
				: '-translate-x-full'}"
		>
			<div class="flex h-14 items-center gap-2.5 border-b border-zinc-200 px-4 dark:border-zinc-800">
				<span
					class="flex h-8 w-8 items-center justify-center rounded-lg bg-brand-600 text-sm font-semibold text-white"
				>
					L
				</span>
				<div class="min-w-0">
					<p class="truncate text-sm font-semibold tracking-tight">lifemail</p>
					<p class="muted truncate text-[11px]">{userSession.version}</p>
				</div>
				<button
					type="button"
					class="{iconButton} ml-auto lg:hidden"
					onclick={() => (mobileOpen = false)}
					aria-label="关闭菜单"
				>
					<Icon name="x" size={16} />
				</button>
			</div>

			<nav class="flex-1 space-y-0.5 overflow-y-auto px-3 py-4">
				{#each nav as item (item.href)}
					<a
						href={hrefFor(item.href)}
						class="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] font-medium transition-colors {isActive(
							item.href
						)
							? 'bg-brand-50 text-brand-700 dark:bg-brand-500/15 dark:text-brand-300'
							: 'text-zinc-600 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100'}"
						aria-current={isActive(item.href) ? 'page' : undefined}
					>
						<Icon name={item.icon} size={16} />
						{item.label}
					</a>
				{/each}
			</nav>

			<div class="space-y-2 border-t border-zinc-200 p-3 dark:border-zinc-800">
				<div class="flex items-center gap-2.5 rounded-lg px-2 py-1.5">
					<span
						class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-zinc-100 text-xs font-semibold text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300"
					>
						{(userSession.profile?.displayName || userSession.profile?.address || '?')
							.trim()
							.slice(0, 1)
							.toUpperCase()}
					</span>
					<div class="min-w-0">
						<p class="truncate text-[13px] font-medium">
							{userSession.profile?.displayName || userSession.profile?.name}
						</p>
						<p class="muted truncate text-[11px]">{userSession.profile?.address}</p>
					</div>
				</div>
				<button
					type="button"
					class="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13px] font-medium text-zinc-600 transition-colors hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
					onclick={logout}
				>
					<Icon name="logout" size={16} />退出登录
				</button>
			</div>
		</aside>

		<div class="flex min-w-0 flex-col">
			<header
				class="sticky top-0 z-20 flex h-14 items-center gap-3 border-b border-zinc-200 bg-zinc-50/85 px-4 backdrop-blur dark:border-zinc-800 dark:bg-zinc-950/80 sm:px-6"
			>
				<button
					type="button"
					class="{iconButton} lg:hidden"
					onclick={() => (mobileOpen = true)}
					aria-label="打开菜单"
				>
					<Icon name="menu" size={18} />
				</button>
				<h1 class="min-w-0 flex-1 truncate text-sm font-semibold tracking-tight">{title}</h1>
				<button
					type="button"
					class={iconButton}
					onclick={() => theme.toggle()}
					aria-label={theme.dark ? '切换到浅色主题' : '切换到深色主题'}
					title={theme.dark ? '浅色主题' : '深色主题'}
				>
					<Icon name={theme.dark ? 'sun' : 'moon'} size={17} />
				</button>
			</header>

			<main class="mx-auto w-full max-w-7xl flex-1 px-4 py-5 sm:px-6 sm:py-6">
				{@render children()}
			</main>
		</div>
	</div>
{/if}
