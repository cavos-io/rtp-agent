import { IconPickerField } from "../IconPicker";

export const FooterBannerTemplate = {
  name: "footerBanner",
  label: "Footer Banner",
  fields: [
    {
      name: "leftText",
      label: "Left Text",
      type: "string",
    },
    {
      name: "leftLinkLabel",
      label: "Left Link Label",
      type: "string",
    },
    {
      name: "leftLinkHref",
      label: "Left Link URL",
      type: "string",
    },
    {
      name: "rightLinks",
      label: "Right Links",
      type: "object",
      list: true,
      ui: {
        itemProps: (item) => ({
          label: item.label || "Link",
        }),
      },
      fields: [
        {
          name: "icon",
          label: "Icon",
          type: "string",
          required: false,
          ui: {
            component: IconPickerField,
          },
        },
        {
          name: "label",
          label: "Label",
          type: "string",
        },
        {
          name: "href",
          label: "URL",
          type: "string",
        },
      ],
    },
  ],
};
