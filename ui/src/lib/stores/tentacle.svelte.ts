import { getPrefix } from '$lib/api/sftp.js';

let prefix = $state('');
let headPrefix = $state('');
let headHost = $state('');
let loaded = $state(false);

export const tentacle = {
	get prefix() { return prefix; },
	get headPrefix() { return headPrefix; },
	get headHost() { return headHost; },
	get loaded() { return loaded; },

	async fetchPrefix() {
		if (loaded) return;
		try {
			const res = await getPrefix();
			prefix = res.prefix;
			headPrefix = res.headPrefix ?? '/home/octo/nas';
			headHost = res.headHost ?? '';
		} catch {
			prefix = '/home/octo/tentacle';
			headPrefix = '/home/octo/nas';
			headHost = '';
		} finally {
			loaded = true;
		}
	}
};
