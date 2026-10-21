<script lang="ts">
	import type { Snippet } from 'svelte';

	type Props = {
		title?: string;
		subtitle?: string;
		actions?: Snippet;
		children: Snippet;
		class?: string;
		padding?: 'none' | 'sm' | 'md';
	};

	let { title, subtitle, actions, children, class: extra = '', padding = 'md' }: Props = $props();

	const paddings: Record<string, string> = {
		none: '',
		sm: 'p-3',
		md: 'p-4 sm:p-5'
	};
</script>

<section class="surface {extra}">
	{#if title || actions}
		<header
			class="flex flex-wrap items-start justify-between gap-3 border-b border-zinc-200 px-4 py-3 dark:border-zinc-800 sm:px-5"
		>
			<div class="min-w-0">
				<h2 class="text-sm font-semibold tracking-tight">{title}</h2>
				{#if subtitle}<p class="muted mt-0.5 text-xs">{subtitle}</p>{/if}
			</div>
			{#if actions}
				<div class="flex shrink-0 items-center gap-2">{@render actions()}</div>
			{/if}
		</header>
	{/if}
	<div class={paddings[padding]}>
		{@render children()}
	</div>
</section>
