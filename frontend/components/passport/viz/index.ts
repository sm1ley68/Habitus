import type { FC } from "react";
import type { Waypoint, Destination, HeroBlockData } from "@/lib/agent/types";
import FamilyDayGraph from "./FamilyDayGraph";
import SocialXrayViz from "./SocialXrayViz";
import InsolationViz from "./InsolationViz";
import EcologyViz from "./EcologyViz";
import NoiseViz from "./NoiseViz";

export type VizProps = {
  metrics: Record<string, number | string>;
  waypoints?: Waypoint[];
  destinations?: Destination[];
  data?: HeroBlockData;
};

// Keyed by LifestyleBlock.key. A block renders its viz above the text only when
// a viz is registered for its key AND block.metrics exists; otherwise text-only.
// Hero keys (family_routing / social_environment / view_and_climate) drive full
// chapters; ecology / quiet render as compact secondary cards.
// Value type includes `undefined` so a lookup for an unregistered key narrows
// correctly (a plain `Record<string, FC>` makes TS treat every miss as defined).
export const VIZ_REGISTRY: Record<string, FC<VizProps> | undefined> = {
  family_routing: FamilyDayGraph,
  social_environment: SocialXrayViz,
  view_and_climate: InsolationViz,
  ecology: EcologyViz,
  quiet: NoiseViz,
};
