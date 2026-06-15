import React, { type ReactNode, useMemo } from 'react';

import { useThemeConfig } from '@docusaurus/theme-common';
import { FooterBanner, type FooterLink } from '@site/src/components/Footer';

/** Represents a single footer config link item */
interface FooterConfigItem {
  label: string;
  to?: string;
  href?: string;
  icon?: string;
}

/** Represents a footer config section with title and items */
interface FooterConfigSection {
  title: string;
  items: FooterConfigItem[];
}

/** Docusaurus footer configuration */
interface DocusaurusFooterConfig {
  style?: 'light' | 'dark';
  links?: FooterConfigSection[];
  logo?: {
    alt?: string;
    src?: string;
    href?: string;
  };
  copyright?: string;
}

/** Docusaurus theme configuration */
interface DocusaurusThemeConfig {
  footer?: DocusaurusFooterConfig;
  [key: string]: unknown;
}

function Footer(): ReactNode {
  const { footer } = useThemeConfig() as DocusaurusThemeConfig;

  if (!footer) {
    return null;
  }

  const { copyright, links } = footer;

  // Custom social links displayed first
  const customRightLinks: FooterLink[] = useMemo(
    () => [
      {
        icon: 'Globe',
        label: 'Cavos Website',
        href: 'https://www.cavos.io',
      },
      {
        icon: 'Linkedin',
        label: 'LinkedIn',
        href: 'https://www.linkedin.com/company/cavos-io',
      },
      // {
      //   icon: 'Github',
      //   label: 'GitHub',
      //   href: 'https://github.com/cavos-io/rtp-agent',
      // },
    ],
    [],
  );

  // Flatten the nested links structure from Docusaurus config
  const configRightLinks: FooterLink[] = useMemo(() => {
    if (!links) return [];

    return links.reduce<FooterLink[]>((acc, section: FooterConfigSection) => {
      if (section.items) {
        const sectionLinks = section.items.map((item) => ({
          label: item.label,
          href: item.href || item.to || '',
          icon: item.icon,
        }));
        return acc.concat(sectionLinks);
      }
      return acc;
    }, []);
  }, [links]);

  // Combine custom links + config links
  const allRightLinks: FooterLink[] = useMemo(
    () => [...customRightLinks, ...configRightLinks],
    [customRightLinks, configRightLinks],
  );
  console.log(copyright);
  
  return (
    <FooterBanner
      leftText="Can't find what you are looking for?"
      leftLinkLabel="Contact Us"
      leftLinkHref="mailto:support@cavos.io"
      rightLinks={allRightLinks}
      copyright={copyright}
    />
  );
}

export default React.memo(Footer);