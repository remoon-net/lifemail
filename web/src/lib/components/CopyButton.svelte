<script lang="ts">
	import Icon from './Icon.svelte';
	import { toast } from '#lib/toast.svelte';

	type Props = { value: string; label?: string };

	let { value, label = '复制' }: Props = $props();
	let copied = $state(false);

	async function copy() {
		try {
			await navigator.clipboard.writeText(value);
		} catch {
			// Clipboard access can be denied outside a secure context; fall back
			// to selecting the text so the user can copy it manually.
			const area = document.createElement('textarea');
			area.value = value;
			document.body.appendChild(area);
			area.select();
			document.execCommand('copy');
			area.remove();
		}
		copied = true;
		toast.ok('已复制到剪贴板');
		setTimeout(() => (copied = false), 1600);
	}
</script>

<button
	type="button"
	class="inline-flex h-8 items-center gap-1.5 rounded-lg border border-zinc-300 bg-white px-2.5 text-xs font-medium text-zinc-600 transition-colors hover:bg-zinc-50 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-300 dark:hover:bg-zinc-800"
	onclick={copy}
	aria-label={label}
>
	<Icon name={copied ? 'check' : 'copy'} size={14} />
	{copied ? '已复制' : label}
</button>
