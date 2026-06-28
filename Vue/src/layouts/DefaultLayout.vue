<template>
  <el-container class="layout">
    <el-header class="layout__header">
      <div class="layout__title">
        <el-icon :size="22"><SetUp /></el-icon>
        <span>{{ t('common.title') }}</span>
      </div>

      <div class="layout__header-right">
        <el-tag size="small" type="info">v{{ version }}</el-tag>
        <!-- 右上角语言切换 -->
        <el-dropdown trigger="click" @command="onLocaleChange">
          <span class="layout__locale">
            <el-icon><Promotion /></el-icon>
            {{ currentLocaleLabel }}
            <el-icon class="el-icon--right"><ArrowDown /></el-icon>
          </span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item
                v-for="l in supportedLocales"
                :key="l.value"
                :command="l.value"
                :class="{ 'layout__locale--active': l.value === locale }"
              >
                {{ l.label }}
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </el-header>

    <el-main class="layout__main">
      <slot />
    </el-main>

    <el-footer class="layout__footer">
      <span>{{ t('footer.producedBy', { brand: 'NPanel.dev' }) }}</span>
      <el-divider direction="vertical" />
      <a
        :href="githubUrl"
        target="_blank"
        rel="noopener noreferrer"
        class="layout__github"
      >
        <el-icon><Link /></el-icon>
        {{ t('footer.github') }}
      </a>
    </el-footer>
  </el-container>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ArrowDown,
  Link,
  Promotion,
  SetUp,
} from '@element-plus/icons-vue'
import { setLocale, supportedLocales, type Locale } from '@/locales'

const { t, locale } = useI18n()

const version = '0.1.0'
const githubUrl = 'https://github.com/npanel-dev/Npanel-migrator'

const currentLocaleLabel = computed(
  () => supportedLocales.find((l) => l.value === locale.value)?.label ?? '',
)

function onLocaleChange(value: Locale) {
  setLocale(value)
}
</script>

<style scoped lang="scss">
.layout {
  height: 100%;

  &__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: #fff;
    border-bottom: 1px solid #e4e7ed;
    box-shadow: 0 1px 4px rgba(0, 21, 41, 0.08);
  }

  &__title {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 18px;
    font-weight: 600;
    color: #303133;
  }

  &__header-right {
    display: flex;
    align-items: center;
    gap: 16px;
  }

  &__locale {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    cursor: pointer;
    color: #606266;
    font-size: 14px;
    outline: none;

    &:hover {
      color: #409eff;
    }

    &--active {
      color: #409eff;
      font-weight: 600;
    }
  }

  &__main {
    flex: 1;
    background: #f0f2f5;
    padding: 24px;
    overflow-y: auto;
  }

  &__footer {
    display: flex;
    align-items: center;
    gap: 4px;
    height: 48px;
    background: #fff;
    border-top: 1px solid #e4e7ed;
    color: #909399;
    font-size: 13px;
    padding: 0 24px;
  }

  &__github {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: #909399;
    text-decoration: none;
    transition: color 0.2s;

    &:hover {
      color: #409eff;
    }
  }
}
</style>
