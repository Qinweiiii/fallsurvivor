import Image from 'next/image';

const FIGURES = [
  { src: '/icon/hlkt.png', alt: '红色蝴蝶结卡通形象（仅个人娱乐使用）' },
  { src: '/icon/mld.png', alt: '粉色兔子形象（仅个人娱乐使用）' },
  { src: '/icon/krm.png', alt: '紫色小恶魔形象（仅个人娱乐使用）' },
  { src: '/icon/pcc.png', alt: '米色小狗形象（仅个人娱乐使用）' },
  { src: '/icon/piano.png', alt: '黄色小狗贝雷帽形象（仅个人娱乐使用）' },
  { src: '/icon/pppr.png', alt: '粉色小羊形象（仅个人娱乐使用）' },
  { src: '/icon/cnmr.png', alt: '白色长耳小狗形象（仅个人娱乐使用）' },
] as const;

/**
 * 个人图鉴：把用户放在 public/icon/ 的 7 张形象作为"个人收藏"展示。
 *
 * 注意：
 *   1. 产品视觉门面（Sidebar logo / HeroCard / favicon）使用自绘素材，
 *      本组件仅作个人娱乐用途；
 *   2. 展示区明确标注用途，避免被截图传播时产生误解；
 *   3. 图片均为本地静态资源，且 next/image 已关闭远程优化。
 */
export function MyFigures({ className, size = 56 }: { className?: string; size?: number }) {
  return (
    <section
      className={
        'card flex flex-col gap-2 border-dashed border-lilac-200 bg-white/60 px-5 py-4 backdrop-blur-sm ' +
        (className ?? '')
      }
      aria-label="个人图鉴（仅作娱乐装饰）"
    >
      <header className="flex items-baseline justify-between">
        <h3 className="text-sm font-semibold text-ink-800">我的收藏小图鉴</h3>
        <span className="text-[11px] text-ink-400">仅作个人娱乐</span>
      </header>

      <p className="text-[11px] leading-relaxed text-ink-500">
        这些图像仅用于个人使用的视觉装饰，不构成产品的官方素材。
        产品 logo、吉祥物与对外视觉均使用原创图像。
      </p>

      <div className="flex flex-wrap items-end gap-3 pt-1">
        {FIGURES.map((f) => (
          <span
            key={f.src}
            className="group relative flex items-center justify-center rounded-2xl bg-white/80 p-1 shadow-card ring-1 ring-lilac-100 transition hover:scale-105"
            title={f.alt}
          >
            <Image
              src={f.src}
              alt={f.alt}
              width={size}
              height={size}
              unoptimized
              className="object-contain"
            />
          </span>
        ))}
      </div>
    </section>
  );
}