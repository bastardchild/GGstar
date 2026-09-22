/* Achievements page Alpine app. Initial identity comes from window.ACH_INIT (inline bootstrap). */
function achievements() {
  return {
    username: (window.ACH_INIT && window.ACH_INIT.username) || '',
    viewerLogin: (window.ACH_INIT && window.ACH_INIT.viewerLogin) || '',
    achievements: [],
    unlockedCount: 0,
    loading: false,
    loaded: false,
    error: '',

    init() {
      if (this.username) this.load();
    },

    async load() {
      const name = (this.username || '').trim();
      if (!name) return;

      this.loading = true;
      this.error = '';
      this.loaded = false;

      try {
        const res = await fetch('/api/achievements/' + encodeURIComponent(name), { credentials: 'same-origin' });
        if (res.status === 401) {
          window.location.href = '/auth/github';
          return;
        }
        const data = await res.json();
        if (!res.ok) throw new Error(data.error || 'Could not load achievements');

        this.achievements = data.achievements || [];
        this.unlockedCount = this.achievements.filter(a => a.unlocked).length;
        this.loaded = true;
      } catch (e) {
        this.error = e.message || 'Could not load achievements';
      } finally {
        this.loading = false;
      }
    }
  };
}
