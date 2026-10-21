<script lang="ts">
	type Props = { rows?: number };
	let { rows = 3 }: Props = $props();
</script>

<div class="space-y-2" aria-busy="true" aria-label="加载中">
	{#each Array(rows) as _, i}
		<div
			class="h-10 animate-pulse rounded-lg bg-zinc-100 dark:bg-zinc-800/70"
			style={`opacity:${1 - i * 0.15}`}
		></div>
	{/each}
</div>
