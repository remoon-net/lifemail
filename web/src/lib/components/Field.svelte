<script lang="ts">
	import type { Snippet } from 'svelte';

	type Props = {
		label: string;
		hint?: string;
		error?: string;
		for?: string;
		children: Snippet;
		class?: string;
	};

	let { label, hint, error, for: htmlFor, children, class: extra = '' }: Props = $props();
</script>

<div class="space-y-1.5 {extra}">
	<label class="block text-xs font-medium text-zinc-600 dark:text-zinc-400" for={htmlFor}>
		{label}
	</label>
	{@render children()}
	{#if error}
		<p class="text-xs text-red-600 dark:text-red-400">{error}</p>
	{:else if hint}
		<p class="muted text-xs">{hint}</p>
	{/if}
</div>
