interface LogoProps {
  className?: string
  title?: string
}

const BAR_ANGLES = [0, 45, 90, 135, 180, 225, 270, 315]

/**
 * Capstan mark — top-down capstan drum (hub, eight bars, coiled-rope ring).
 * Adapts to light/dark via Tailwind `dark:` variants so it inherits the app theme.
 */
export function Logo({ className, title = 'Capstan' }: LogoProps) {
  return (
    <svg viewBox="0 0 100 100" role="img" aria-label={title} className={className}>
      <circle
        cx="50"
        cy="50"
        r="25"
        fill="none"
        strokeWidth="3"
        strokeDasharray="3 4.5"
        className="stroke-[#0d8a8a] dark:stroke-[#2bb8b8]"
      />
      {BAR_ANGLES.map((deg) => (
        <g key={deg} transform={`rotate(${deg} 50 50)`}>
          <rect
            x="47.6"
            y="7"
            width="4.8"
            height="33"
            rx="2.4"
            className="fill-[#0f3a4d] dark:fill-[#e9e2d0]"
          />
          <circle
            cx="50"
            cy="7.5"
            r="4"
            strokeWidth="1.4"
            className="fill-[#d9a441] stroke-[#0f3a4d] dark:stroke-transparent"
          />
        </g>
      ))}
      <circle cx="50" cy="50" r="15" className="fill-[#0f3a4d] dark:fill-[#e9e2d0]" />
      <circle cx="50" cy="50" r="6" className="fill-[#d9a441]" />
    </svg>
  )
}
