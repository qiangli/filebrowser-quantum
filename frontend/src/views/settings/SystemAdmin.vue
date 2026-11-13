<template>
  <div class="card-content">
    <div class="settings-items">
      <ToggleSwitch class="item" v-model="localuser.disableUpdateNotifications" @change="updateSettings"
        :name="$t('profileSettings.disableUpdateNotifications')"
        :description="$t('profileSettings.disableUpdateNotificationsDescription')" />
      <SettingsButton class="item" :name="$t('settings.configViewerOpen')"
        :description="$t('settings.configViewerDescription')" @click="openConfigViewerPrompt" />
    </div>
  </div>
</template>

<script>
import { notify } from "@/notify";
import { state, mutations } from "@/store";
import ToggleSwitch from "@/components/settings/ToggleSwitch.vue";
import SettingsButton from "@/components/settings/SettingsButton.vue";

export default {
  name: "systemAdmin",
  components: {
    ToggleSwitch,
    SettingsButton,
  },
  data() {
    return {
      localuser: { disableUpdateNotifications: false },
    };
  },
  async mounted() {
    this.localuser = {
      disableUpdateNotifications: false,
      ...state.user,
    };
  },
  methods: {
    async updateSettings(event) {
      if (event && typeof event.preventDefault === "function") {
        event.preventDefault();
      }
      try {
        void mutations.updateCurrentUser(this.localuser);
        notify.showSuccessToast(this.$t("settings.settingsUpdated"));
      } catch (e) {
        console.error(e);
      }
    },
    openConfigViewerPrompt() {
      mutations.showPrompt({
        name: "config-viewer",
        props: {
          title: this.$t("settings.configViewer"),
        },
      });
    },
  },
};
</script>

<style scoped>
.card-content {
  margin-top: 1em;
}
</style>
