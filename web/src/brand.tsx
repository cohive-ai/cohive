import { useId } from "react";

// 品牌标志，图形与 docs/brand 的 SVG 相同：外框和人的格子随文字颜色，智能体格子固定用品牌黄
export function BrandMark({ className = "" }: { className?: string }) {
  return (
    <svg viewBox="0 0 100 100" className={className} aria-hidden="true">
      <polyline
        points="88.1,28 50,6 11.9,28 11.9,72 50,94 88.1,72"
        fill="none"
        stroke="currentColor"
        strokeWidth="10"
        strokeLinejoin="miter"
      />
      <polygon points="39,37.5 49.83,43.75 49.83,56.25 39,62.5 28.17,56.25 28.17,43.75" fill="currentColor" />
      <polygon points="63,37.5 73.83,43.75 73.83,56.25 63,62.5 52.17,56.25 52.17,43.75" fill="var(--brand-yellow)" />
    </svg>
  );
}

// 字标：纯几何笔画，不依赖字体；clipPath 的编号每个实例各不相同，同页多处使用不会串
export function BrandWordmark({ className = "" }: { className?: string }) {
  const clip = useId();
  return (
    <svg viewBox="-2 -2 172 24" className={className} role="img" aria-label="Cohive">
      <defs>
        <clipPath id={clip}>
          <rect x="-2" y="-2" width="174" height="24" />
        </clipPath>
      </defs>
      <g fill="none" stroke="currentColor" strokeWidth="4" strokeLinejoin="miter" clipPath={`url(#${clip})`}>
        <polyline points="24,0 6,0 0,6 0,14 6,20 24,20" />
        <polygon points="40,0 52,0 58,6 58,14 52,20 40,20 34,14 34,6" />
        <path d="M68 -2V22M92 -2V22M68 10H92M102 -2V22M146 10H164" />
        <polyline points="112,-3 124,18.1 136,-3" />
        <polyline points="168,0 146,0 146,20 168,20" />
      </g>
    </svg>
  );
}
