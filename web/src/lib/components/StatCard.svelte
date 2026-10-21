<script lang="ts">
	import Icon from './Icon.svelte';
	import type { IconName } from '$lib/icons';

	type Props = {
		label: string;
		value: string | number;
		hint?: string;
		icon?: IconName;
		tone?: 'default' | 'warn' | 'danger' | 'ok';
	};

	let { label, value, hint, icon = 'activity', tone = 'default' }: Props = $props();

	const tones: Record<string, string> = {
		default: 'text-zinc-900 dark:text-zinc-100',
		warn: 'text-amber-600 dark:text-amber-400',
		danger: 'text-red-600 dark:text-red-400',
		ok: 'text-emerald-600 dark:text-emerald-400'
	};
</script>

<div class="surface flex items-start justify-between gap-3 p-4">
	<div class="min-w-0">
		<p class="muted text-xs font-medium">{label}</p>
		<p class="tabular mt-1 text-2xl font-semibold tracking-tight {tones[tone]}">{value}</p>
		{#if hint}<p class="muted mt-0.5 truncate text-xs">{hint}</p>{/if}
	</div>
	<span
		class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-brand-50 text-brand-600 dark:bg-brand-500/15 dark:text-brand-300"
	>
		<Icon name={icon} size={17} />
	</span>
</div>
