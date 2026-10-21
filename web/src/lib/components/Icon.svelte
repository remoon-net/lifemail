<script lang="ts">
	import { icons, type IconName } from '$lib/icons';

	type Props = {
		name: IconName;
		size?: number;
		stroke?: number;
		class?: string;
	};

	let { name, size = 18, stroke = 1.8, class: extra = '' }: Props = $props();
</script>

<svg
	xmlns="http://www.w3.org/2000/svg"
	width={size}
	height={size}
	viewBox="0 0 24 24"
	fill="none"
	stroke="currentColor"
	stroke-width={stroke}
	stroke-linecap="round"
	stroke-linejoin="round"
	aria-hidden="true"
	class={extra}
>
	{@html icons[name]}
</svg>
