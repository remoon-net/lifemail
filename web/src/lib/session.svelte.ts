import { api, ApiError } from './api';

class SessionStore {
	ready = $state(false);
	authenticated = $state(false);
	version = $state('dev');
	error = $state('');

	async boot(): Promise<void> {
		try {
			const info = await api.session();
			this.authenticated = info.authenticated;
			this.version = info.version || 'dev';
		} catch {
			this.authenticated = false;
		} finally {
			this.ready = true;
		}
	}

	async login(token: string): Promise<void> {
		this.error = '';
		try {
			const info = await api.login(token);
			this.authenticated = info.authenticated;
			this.version = info.version || 'dev';
		} catch (err) {
			if (err instanceof ApiError && err.status === 401) {
				this.error = 'Token 无效';
			} else {
				this.error = err instanceof Error ? err.message : '登录失败';
			}
			throw err;
		}
	}

	async logout(): Promise<void> {
		try {
			await api.logout();
		} catch {
			// A stale cookie is as good as logged out.
		}
		this.authenticated = false;
	}

	markUnauthenticated(): void {
		this.authenticated = false;
	}
}

export const session = new SessionStore();
