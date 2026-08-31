import type { Config } from 'tailwindcss';

/**
 * 视觉语言：梦幻、轻盈、明亮、低饱和度。
 *
 * 参考图风格方向：极淡的多色背景（紫/粉/蓝/黄），圆角很柔的卡片，
 * 大量留白，漂浮的小装饰元素。原始吉祥物来自 images/decor/。
 *
 * 信息密度、表格、筛选器、Drawer 以求职管理场景为第一优先级，
 * 不为了追求"可爱"而牺牲信息可读性。
 */
const config: Config = {
  content: ['./src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // 主色：薰衣草紫
        lilac: {
          50: '#faf8ff',
          100: '#f3eeff',
          200: '#e6dcff',
          300: '#d3c2fb',
          400: '#b79df3',
          500: '#9b7ae8',
          600: '#7f5ad4',
          700: '#6845b0',
          800: '#523787',
          900: '#3d2a63',
        },
        // 樱花粉（比 blossom 更柔更亮）
        blossom: {
          50: '#fff7fb',
          100: '#ffeaf4',
          200: '#ffd4e8',
          300: '#ffb3d5',
          400: '#fd8cbc',
          500: '#f4649f',
          600: '#dc4681',
          700: '#b53566',
          800: '#8c294f',
          900: '#6a1f3c',
        },
        // 天空蓝
        sky: {
          50: '#f5fbff',
          100: '#e7f4ff',
          200: '#cfe8ff',
          300: '#a8d5ff',
          400: '#75bbfa',
          500: '#4c9eef',
          600: '#2e7fd4',
          700: '#2264aa',
          800: '#1c4e83',
          900: '#173c63',
        },
        // 奶油黄（新增，用于参考图风格）
        cream: {
          50: '#fffdf5',
          100: '#fff8e3',
          200: '#ffeec1',
          300: '#ffe19a',
          400: '#ffd174',
          500: '#f5b83d',
          600: '#d99727',
          700: '#a86d19',
          800: '#7a4e10',
          900: '#503309',
        },
        // 薄荷绿
        mint: {
          50: '#effaf3',
          100: '#e6f9f1',
          200: '#c9efdb',
          300: '#a5e8cd',
          400: '#73d8b5',
          500: '#4fc79b',
          600: '#34a37c',
          700: '#2e8f6c',
          800: '#1f6550',
          900: '#114434',
        },
        // 中性色偏暖
        ink: {
          50: '#f8f8fa',
          100: '#f1f1f5',
          200: '#e4e4ec',
          300: '#cdcdd9',
          400: '#9d9dae',
          500: '#73738a',
          600: '#55556a',
          700: '#414153',
          800: '#2c2c3a',
          900: '#1d1d27',
        },
      },
      borderRadius: {
        card: '1.25rem',
        pill: '999px',
      },
      boxShadow: {
        soft: '0 4px 20px -6px rgba(155, 122, 232, 0.18)',
        card: '0 2px 14px -4px rgba(155, 122, 232, 0.14)',
        lift: '0 12px 32px -12px rgba(155, 122, 232, 0.28)',
        glow: '0 8px 28px -8px rgba(253, 140, 188, 0.32)',
      },
      fontFamily: {
        sans: [
          'var(--font-sans)',
          'system-ui',
          '-apple-system',
          'PingFang SC',
          'Hiragino Sans GB',
          'Microsoft YaHei',
          'sans-serif',
        ],
      },
      backgroundImage: {
        // 多层径向渐变：让背景看起来像柔和的彩色光晕，参考图风格
        dreamy:
          'radial-gradient(60% 60% at 12% 8%, rgba(214, 198, 250, 0.55), transparent 65%), radial-gradient(48% 48% at 88% 4%, rgba(255, 198, 222, 0.50), transparent 65%), radial-gradient(50% 50% at 6% 95%, rgba(255, 224, 158, 0.45), transparent 65%), radial-gradient(55% 55% at 92% 90%, rgba(180, 215, 255, 0.45), transparent 65%), linear-gradient(135deg, #faf8ff 0%, #fff8fb 45%, #f5fbff 100%)',
        // 卡片用渐变（淡紫→淡粉）
        'card-lilac':
          'linear-gradient(135deg, rgba(214, 198, 250, 0.55) 0%, rgba(255, 213, 232, 0.55) 100%)',
        'card-mint':
          'linear-gradient(135deg, rgba(201, 239, 219, 0.55) 0%, rgba(168, 213, 255, 0.55) 100%)',
        'card-cream':
          'linear-gradient(135deg, rgba(255, 238, 193, 0.55) 0%, rgba(255, 211, 116, 0.35) 100%)',
        'card-sky':
          'linear-gradient(135deg, rgba(207, 232, 255, 0.55) 0%, rgba(214, 198, 250, 0.55) 100%)',
        // 按钮渐变
        'btn-primary':
          'linear-gradient(135deg, #9b7ae8 0%, #f4649f 100%)',
      },
      keyframes: {
        'fade-in': {
          from: { opacity: '0', transform: 'translateY(4px)' },
          to: { opacity: '1', transform: 'translateY(0)' },
        },
        'slide-in-right': {
          from: { transform: 'translateX(100%)' },
          to: { transform: 'translateX(0)' },
        },
        float: {
          '0%, 100%': { transform: 'translateY(0) rotate(0)' },
          '50%': { transform: 'translateY(-8px) rotate(2deg)' },
        },
        floatAlt: {
          '0%, 100%': { transform: 'translateY(0) rotate(0)' },
          '50%': { transform: 'translateY(6px) rotate(-2deg)' },
        },
        sparkle: {
          '0%, 100%': { opacity: '0.55', transform: 'scale(1)' },
          '50%': { opacity: '1', transform: 'scale(1.2)' },
        },
      },
      animation: {
        'fade-in': 'fade-in 0.24s ease-out',
        'slide-in-right': 'slide-in-right 0.28s cubic-bezier(0.16, 1, 0.3, 1)',
        float: 'float 5s ease-in-out infinite',
        floatAlt: 'floatAlt 6.5s ease-in-out infinite',
        sparkle: 'sparkle 2.4s ease-in-out infinite',
      },
    },
  },
  plugins: [],
};

export default config;