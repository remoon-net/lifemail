export type ToastKind = 'ok' | 'error' | 'info';

export interface Toast {
	id: number;
	text: string;
	kind: ToastKind;
}

class ToastStore {
	items = $state<Toast[]>([]);
	#next = 1;

	push(text: string, kind: ToastKind = 'ok'): void {
		const item: Toast = { id: this.#next++, text, kind };
		this.items = [...this.items, item];
		setTimeout(() => this.dismiss(item.id), kind === 'error' ? 6000 : 3200);
	}

	ok(text: string): void {
		this.push(text, 'ok');
	}

	fail(err: unknown, fallback = ''): void {
		const message = err instanceof Error ? err.message : String(err ?? '');
		this.push(fallback || message || '操作失败', 'error');
	}

	dismiss(id: number): void {
		this.items = this.items.filter((item) => item.id !== id);
	}
}

export const toast = new ToastStore();
