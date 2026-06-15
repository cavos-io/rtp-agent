import React, { FC } from 'react';
import Link from '@docusaurus/Link';
import * as LucideIcons from 'lucide-react';

/** Represents a single link item in the footer */
export interface FooterLink {
  label: string;
  href: string;
  icon?: string;
}

/** Props for the FooterBanner component */
interface FooterBannerProps {
  /** Text displayed on the left side of the footer */
  leftText?: string;
  /** Label for the left side link */
  leftLinkLabel?: string;
  /** URL for the left side link */
  leftLinkHref?: string;
  /** Array of links to display on the right side */
  rightLinks?: FooterLink[];
  /** Copyright text */
  copyright?: string;
}

/**
 * FooterBanner Component
 * 
 * A customizable footer component that displays:
 * - Left side: Custom text and optional link
 * - Right side: Array of footer links with optional icons
 * 
 * @example
 * ```tsx
 * <FooterBanner
 *   leftText="Can't find what you're looking for?"
 *   leftLinkLabel="Contact Us"
 *   leftLinkHref="mailto:support@cavos.io"
 *   rightLinks={[
 *     { icon: "Globe", label: "Website", href: "https://cavos.io" },
 *     { icon: "Linkedin", label: "LinkedIn", href: "https://linkedin.com/company/cavos-io" }
 *   ]}
 *   copyright="Copyright © 2026 Cavos."
 * />
 * ```
 */
export const FooterBanner: FC<FooterBannerProps> = ({
  leftText,
  leftLinkLabel,
  leftLinkHref,
  rightLinks = [],
  copyright,
}) => {
  return (
    <div className="footer-wrapper">
      <div className="footer-inner">

        {/* Left side */}
        <div className="footer-left">
          {leftText && <span className="footer-left-text">{leftText} </span>}
          {leftLinkHref && (
            <Link href={leftLinkHref} className="footer-left-text">
              {leftLinkLabel}
            </Link>
          )}
        </div>

        {/* Right side */}
        <div className="footer-right ">
          {rightLinks.map((item, index) => {
            const IconComponent = item.icon 
              ? (LucideIcons[item.icon as keyof typeof LucideIcons] as React.ComponentType<{ size: number }>) 
              : null;
            const isLinkedin = item.icon && String(item.icon).toLowerCase() === 'linkedin';
            
            return (
              <Link key={index} href={item.href} className="footer-right-link">
                {isLinkedin ? (
                  <img 
                    src="/rtp-agent/img/linkedin-logo.svg" 
                    alt="LinkedIn" 
                    style={{ width: 16, height: 16, marginRight: 6 }} 
                  />
                ) : (
                  IconComponent && <IconComponent size={16} />
                )}
                {item.label}
              </Link>
            );
          })}
        </div>

      </div>

      {/* Separator line */}
      <div className="footer-divider" />

      {/* Copyright section */}
      {copyright && (
        <div className="footer-copyright">
          {copyright}
        </div>
      )}
    </div>
  );
};