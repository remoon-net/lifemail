import { mailApi, MailApiError } from './mail-api';
import type { UserProfile } from './mail-types';

class UserSessionStore {
	ready = $state(false);
	authenticated = $state(false);
	profile = $state<UserProfile | null>(null);
	version = $state('dev');
	error = $state('');

	async boot(): Promise<void> {
		try {
			const info = await mailApi.session();
			this.authenticated = info.authenticated;
			this.profile = info.user ?? null;
			this.version = info.version || 'dev';
		} catch {
			this.authenticated = false;
			this.profile = null;
		} finally {
			this.ready = true;
		}
	}

	async login(login: string, password: string): Promise<void> {
		this.error = '';
		try {
			const info = await mailApi.login(login, password);
			this.authenticated = info.authenticated;
			this.profile = info.user ?? null;
			this.version = info.version || 'dev';
		} catch (err) {
			if (err instanceof MailApiError && err.status === 401) {
				this.error = '账号或密码不正确';
			} else if (err instanceof MailApiError && err.status === 503) {
				this.error = '服务端未启用网页登录（缺少管理员 Token）';
			} else {
				this.error = err instanceof Error ? err.message : '登录失败';
			}
			throw err;
		}
	}

	async logout(): Promise<void> {
		try {
			await mailApi.logout();
		} catch {
			// A stale cookie is as good as logged out.
		}
		this.authenticated = false;
		this.profile = null;
	}

	markUnauthenticated(): void {
		this.authenticated = false;
		this.profile = null;
	}
}

export const userSession = new UserSessionStore();
