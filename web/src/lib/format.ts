const relative = new Intl.RelativeTimeFormat('zh-CN', { numeric: 'auto' });

const UNITS: Array<{ limit: number; unit: Intl.RelativeTimeFormatUnit; ms: number }> = [
	{ limit: 60_000, unit: 'second', ms: 1000 },
	{ limit: 3_600_000, unit: 'minute', ms: 60_000 },
	{ limit: 86_400_000, unit: 'hour', ms: 3_600_000 },
	{ limit: 2_592_000_000, unit: 'day', ms: 86_400_000 },
	{ limit: 31_536_000_000, unit: 'month', ms: 2_592_000_000 },
	{ limit: Number.POSITIVE_INFINITY, unit: 'year', ms: 31_536_000_000 }
];

/** Formats a unix timestamp (seconds) as "3 分钟前". */
export function relativeTime(unix: number): string {
	if (!unix) return '—';
	const delta = unix * 1000 - Date.now();
	const abs = Math.abs(delta);
	for (const step of UNITS) {
		if (abs < step.limit) {
			return relative.format(Math.round(delta / step.ms), step.unit);
		}
	}
	return '—';
}

export function dateTime(unix: number): string {
	if (!unix) return '—';
	return new Date(unix * 1000).toLocaleString('zh-CN', { hour12: false });
}

export function shortDate(unix: number): string {
	if (!unix) return '—';
	return new Date(unix * 1000).toLocaleDateString('zh-CN');
}

export function bytes(size: number): string {
	if (!size) return '0 B';
	if (size < 1024) return `${size} B`;
	if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KiB`;
	if (size < 1024 * 1024 * 1024) return `${(size / 1024 / 1024).toFixed(1)} MiB`;
	return `${(size / 1024 / 1024 / 1024).toFixed(2)} GiB`;
}

/** Deterministic hue so the same account always gets the same avatar colour. */
export function hueFor(value: string): number {
	let hash = 0;
	for (let i = 0; i < value.length; i++) {
		hash = (hash * 31 + value.charCodeAt(i)) % 360;
	}
	return hash;
}

export function initials(value: string): string {
	const trimmed = value.trim();
	if (!trimmed) return '?';
	return trimmed.slice(0, 1).toUpperCase();
}
