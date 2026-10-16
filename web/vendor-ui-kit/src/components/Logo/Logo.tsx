import React from 'react'
import logo256 from '../../assets/logo/logo-256.png'
import logo512 from '../../assets/logo/logo-512.png'
import logo1000 from '../../assets/logo/logo-1000.png'

export type LogoSize = 'small' | 'medium' | 'large' | number

interface LogoProps extends React.ImgHTMLAttributes<HTMLImageElement> {
  size?: LogoSize
}

const getLogoSource = (size: LogoSize) => {
  if (typeof size === 'number') {
    if (size <= 256) return logo256
    if (size <= 512) return logo512
    return logo1000
  }
  
  switch (size) {
    case 'small': return logo256  // 32-64px usage
    case 'medium': return logo512 // 128-256px usage
    case 'large': return logo1000 // Hero/Splash usage
    default: return logo256
  }
}

const getSizeStyle = (size: LogoSize): React.CSSProperties => {
  if (typeof size === 'number') {
    return { width: size, height: size }
  }
  
  switch (size) {
    case 'small': return { width: 48, height: 48 }
    case 'medium': return { width: 128, height: 128 }
    case 'large': return { width: 256, height: 256 }
    default: return { width: 48, height: 48 }
  }
}

export const Logo: React.FC<LogoProps> = ({ size = 'small', style, alt = '3eAI Labs Logo', ...props }) => {
  const imgSrc = getLogoSource(size)
  const sizeStyle = getSizeStyle(size)

  return (
    <img
      src={imgSrc}
      alt={alt}
      style={{ objectFit: 'contain', ...sizeStyle, ...style }}
      {...props}
    />
  )
}
