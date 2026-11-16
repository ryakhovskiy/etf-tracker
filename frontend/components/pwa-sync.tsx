"use client";
import { useEffect } from "react";
import { initPwa } from "../lib/pwa";
import { UpdateNotice } from "./update-notice";

/** Registers the service worker (production only) and shows the update notice. Mounted once in the root layout. */
export function PwaSync() {
  useEffect(() => { initPwa(); }, []);
  return <UpdateNotice />;
}
