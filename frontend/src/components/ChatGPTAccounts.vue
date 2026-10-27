<!-- SPDX-License-Identifier: GPL-3.0-only -->
<template>
  <section class="chatgpt-accounts">
    <div class="chatgpt-title">ChatGPT</div>
    <p>{{ t('chatgpt.description') }}</p>
    <n-select :value="selected" :options="options" :placeholder="t('chatgpt.account')" :disabled="busy || loggingOut" @update:value="selectAccount" />
    <p v-if="account">{{ account.email || account.subject }} · {{ t(account.signedIn ? (account.planEnabled ? 'chatgpt.authorized' : 'chatgpt.noScope') : 'chatgpt.signedOut') }}</p>
    <n-space style="margin-top: 10px">
      <n-button :loading="busy" :disabled="busy || loggingOut" @click="login('')">{{ busy ? t('chatgpt.signingIn') : t('chatgpt.login') }}</n-button>
      <n-button v-if="busy" @click="cancelLogin">{{ t('common.cancel') }}</n-button>
      <n-button v-if="account" :disabled="busy || loggingOut" @click="login(selected, !account.planEnabled)">{{ t(account.planEnabled ? 'chatgpt.reauthorize' : 'chatgpt.enablePlan') }}</n-button>
      <n-button v-if="account?.signedIn" :disabled="busy || loggingOut" :loading="loggingOut" @click="logout">{{ t('chatgpt.logout') }}</n-button>
      <n-button size="small" secondary :loading="loadingModels" :disabled="!account?.planEnabled || busy || loggingOut" @click="loadModels">{{ t('chatgpt.refreshModels') }}</n-button>
      <n-button @click="Browser.OpenURL('https://chatgpt.com/settings/usage')">{{ t('chatgpt.usage') }}</n-button>
    </n-space>
    <n-alert v-if="notice" :type="noticeType" style="margin-top: 10px">{{ notice }}</n-alert>
  </section>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { Browser } from '@wailsio/runtime';
import { GetChatGPTAccounts, LoginChatGPT, CancelChatGPTLogin, LogoutChatGPT, FetchChatGPTModels } from '../../bindings/ally-dev/internal/app/app';
import { t } from '../i18n.mjs';

const props = defineProps({ accountId: { type: String, default: '' } });
const emit = defineEmits(['account', 'models']);
const accounts = ref([]);
const selected = ref(props.accountId);
const busy = ref(false);
const loggingOut = ref(false);
const loadingModels = ref(false);
const notice = ref('');
const noticeType = ref('info');
const account = computed(() => accounts.value.find((item) => item.id === selected.value));
const options = computed(() => accounts.value.map((item) => ({ label: `${item.email || item.subject} · ${item.id}`, value: item.id })));
let mounted = true;
let modelEpoch = 0;
function fail(error) { notice.value = String(error?.message || error); noticeType.value = 'error'; }
async function refreshAccounts() {
  accounts.value = await GetChatGPTAccounts();
  if (!accounts.value.some((item) => item.id === selected.value)) selected.value = accounts.value[0]?.id || '';
  emit('account', selected.value);
}
async function selectAccount(id) {
  selected.value = id;
  modelEpoch += 1;
  loadingModels.value = false;
  emit('account', id);
  emit('models', []);
  await loadModels();
}
async function loadModels() {
  const id = selected.value;
  const epoch = ++modelEpoch;
  emit('models', []);
  if (!account.value?.planEnabled) return;
  loadingModels.value = true;
  try {
    const models = await FetchChatGPTModels(id);
    if (!mounted || epoch !== modelEpoch || id !== selected.value) return;
    emit('models', models);
    if (!models.length) { notice.value = t('chatgpt.noModels'); noticeType.value = 'warning'; }
  } catch (err) { if (mounted && epoch === modelEpoch) fail(err); }
  finally { if (epoch === modelEpoch) loadingModels.value = false; }
}
async function login(id, consent = false) {
  busy.value = true; notice.value = ''; modelEpoch += 1;
  try {
    const result = await LoginChatGPT(id, consent);
    if (!mounted) return;
    selected.value = result.id;
    await refreshAccounts();
    notice.value = t(result.planEnabled ? 'chatgpt.success' : 'chatgpt.noScope');
    noticeType.value = result.planEnabled ? 'success' : 'warning';
    await loadModels();
  } catch (err) { if (mounted) fail(err); }
  finally { busy.value = false; }
}
async function cancelLogin() { try { await CancelChatGPTLogin(); } catch (err) { fail(err); } }
async function logout() {
  loggingOut.value = true; modelEpoch += 1; emit('models', []);
  try {
    const result = await LogoutChatGPT(selected.value);
    await refreshAccounts();
    notice.value = t(result.remoteRevoked ? 'chatgpt.loggedOut' : 'chatgpt.localLogout');
    noticeType.value = result.remoteRevoked ? 'success' : 'warning';
  } catch (err) { fail(err); }
  finally { loggingOut.value = false; }
}
onMounted(async () => { try { await refreshAccounts(); await loadModels(); } catch (err) { fail(err); } });
onUnmounted(() => { mounted = false; modelEpoch += 1; if (busy.value) CancelChatGPTLogin().catch(() => {}); });
</script>

<style scoped>
.chatgpt-accounts { padding: 14px; margin-bottom: 16px; border: 1px solid var(--ally-border); border-radius: 8px; }
.chatgpt-title { font-weight: 600; }
p { font-size: 12px; color: var(--ally-text-muted); line-height: 1.6; }
</style>
