const STORAGE_KEY = 'lifemail-theme';

class ThemeStore {
	dark = $state(false);
	#initialized = false;

	init(): void {
		if (this.#initialized || typeof window === 'undefined') return;
		this.#initialized = true;
		const stored = localStorage.getItem(STORAGE_KEY);
		if (stored === 'dark' || stored === 'light') {
			this.dark = stored === 'dark';
		} else {
			this.dark = window.matchMedia('(prefers-color-scheme: dark)').matches;
		}
		this.#apply();
	}

	toggle(): void {
		this.dark = !this.dark;
		localStorage.setItem(STORAGE_KEY, this.dark ? 'dark' : 'light');
		this.#apply();
	}

	#apply(): void {
		if (typeof document === 'undefined') return;
		document.documentElement.classList.toggle('dark', this.dark);
	}
}

export const theme = new ThemeStore();
