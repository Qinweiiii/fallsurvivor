import type { Metadata } from 'next';
import '@/styles/globals.css';
import { Sidebar } from '@/components/layout/Sidebar';
import { PageDecor } from '@/components/decor/Decor';

export const metadata: Metadata = {
  title: 'Fall Survivor · 个人求职工作台',
  description: '战战战杀杀杀 · 大战秋招中',
  // 图标文件放在 public/ 下，使用固定路径引用（不经 Next.js 哈希编译）。
  // favicon.png 为标签页图标（747x747 原图，sizes 需与实际尺寸一致，否则浏览器会跳过）。
  icons: {
    icon: [{ url: '/favicon.png', type: 'image/png', sizes: '747x747' }],
    apple: [{ url: '/apple-icon.png', type: 'image/png', sizes: '180x180' }],
    shortcut: [{ url: '/favicon.png', type: 'image/png' }],
  },
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN">
      <body>
        <div className="flex h-screen overflow-hidden">
          <Sidebar />
          <main className="relative flex min-w-0 flex-1 flex-col overflow-hidden">
            <PageDecor />
            {children}
          </main>
        </div>
      </body>
    </html>
  );
}