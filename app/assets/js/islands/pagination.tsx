import type { ComponentChild } from "react";

import { Pagination } from "../ui/pagination";

// Server-rendered lists mount this island with the current page, total page
// count and the base URL; changing page navigates to base with ?page=N.
export default function PaginationIsland(props: Record<string, unknown>): ComponentChild {
  const page = typeof props.page === "number" ? props.page : 1;
  const pageCount = typeof props.pageCount === "number" ? props.pageCount : 1;
  const base = typeof props.base === "string" ? props.base : location.pathname;

  return (
    <Pagination
      page={page}
      pageCount={pageCount}
      onPageChange={(next) => {
        const url = new URL(base, location.href);
        url.searchParams.set("page", String(next));
        location.assign(url.toString());
      }}
    />
  );
}
