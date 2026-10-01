import { useState, type ReactNode } from "react"
import {
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getSortedRowModel,
  useReactTable,
  type ColumnDef,
  type ColumnFiltersState,
  type FilterFnOption,
  type Row,
  type RowData,
  type SortingState,
} from "@tanstack/react-table"
import { ArrowDownIcon, ArrowUpDownIcon, ArrowUpIcon } from "lucide-react"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { cn } from "@/lib/utils"

declare module "@tanstack/react-table" {
  interface ColumnMeta<TData extends RowData, TValue> {
    /** Classes for both header and cells (e.g. width, alignment). */
    className?: string
    align?: "right" | "center"
  }
}

export interface DataTableProps<T> {
  columns: ColumnDef<T>[]
  data: T[] | undefined
  getRowId: (row: T) => string
  isLoading?: boolean
  empty?: ReactNode
  globalFilter?: string
  globalFilterFn?: FilterFnOption<T>
  columnFilters?: ColumnFiltersState
  initialSorting?: SortingState
  rowClassName?: (row: Row<T>) => string | undefined
  className?: string
  /** Skeleton rows while loading. */
  skeletonRows?: number
}

export function DataTable<T>({
  columns,
  data,
  getRowId,
  isLoading,
  empty,
  globalFilter,
  globalFilterFn = "includesString",
  columnFilters,
  initialSorting = [],
  rowClassName,
  className,
  skeletonRows = 6,
}: DataTableProps<T>) {
  const [sorting, setSorting] = useState<SortingState>(initialSorting)

  // TanStack Table v8 is not React Compiler compatible; this project does not use the compiler.
  // oxlint-disable-next-line react/incompatible-library
  const table = useReactTable<T>({
    data: data ?? [],
    columns,
    getRowId: (row) => getRowId(row),
    state: { sorting, globalFilter: globalFilter ?? "", columnFilters: columnFilters ?? [] },
    onSortingChange: setSorting,
    globalFilterFn,
    enableSortingRemoval: false,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    autoResetAll: false,
  })

  const rows = table.getRowModel().rows
  const colCount = table.getVisibleLeafColumns().length

  return (
    <Table className={cn("min-w-max", className)}>
      <TableHeader className="bg-muted/40">
        {table.getHeaderGroups().map((hg) => (
          <TableRow key={hg.id} className="hover:bg-transparent">
            {hg.headers.map((header) => {
              const meta = header.column.columnDef.meta
              const sorted = header.column.getIsSorted()
              const canSort = header.column.getCanSort()
              const content = header.isPlaceholder
                ? null
                : flexRender(header.column.columnDef.header, header.getContext())
              return (
                <TableHead
                  key={header.id}
                  aria-sort={sorted === "asc" ? "ascending" : sorted === "desc" ? "descending" : undefined}
                  className={cn(
                    "h-10 text-xs font-medium text-muted-foreground",
                    meta?.align === "right" && "text-right",
                    meta?.align === "center" && "text-center",
                    meta?.className,
                  )}
                >
                  {canSort ? (
                    <button
                      type="button"
                      onClick={header.column.getToggleSortingHandler()}
                      className={cn(
                        "-mx-1 inline-flex items-center gap-1 rounded px-1 py-0.5 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none",
                        meta?.align === "right" && "flex-row-reverse",
                        sorted && "text-foreground",
                      )}
                    >
                      {content}
                      {sorted === "asc" ? (
                        <ArrowUpIcon className="size-3" />
                      ) : sorted === "desc" ? (
                        <ArrowDownIcon className="size-3" />
                      ) : (
                        <ArrowUpDownIcon className="size-3 opacity-40" />
                      )}
                    </button>
                  ) : (
                    content
                  )}
                </TableHead>
              )
            })}
          </TableRow>
        ))}
      </TableHeader>
      <TableBody>
        {isLoading && !data ? (
          Array.from({ length: skeletonRows }, (_, i) => (
            <TableRow key={`sk-${i}`} className="hover:bg-transparent">
              {Array.from({ length: colCount }, (_, j) => (
                <TableCell key={j}>
                  <Skeleton className="h-5 w-full min-w-12" />
                </TableCell>
              ))}
            </TableRow>
          ))
        ) : rows.length === 0 ? (
          <TableRow className="hover:bg-transparent">
            <TableCell colSpan={colCount} className="p-0">
              <div className="sticky left-0 w-[min(100%,100vw)] max-w-[calc(100vw-2rem)]">{empty ?? <p className="p-8 text-center text-sm text-muted-foreground">No results.</p>}</div>
            </TableCell>
          </TableRow>
        ) : (
          rows.map((row) => (
            <TableRow key={row.id} className={rowClassName?.(row)}>
              {row.getVisibleCells().map((cell) => {
                const meta = cell.column.columnDef.meta
                return (
                  <TableCell
                    key={cell.id}
                    className={cn(
                      "py-2",
                      meta?.align === "right" && "tabular text-right",
                      meta?.align === "center" && "text-center",
                      meta?.className,
                    )}
                  >
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </TableCell>
                )
              })}
            </TableRow>
          ))
        )}
      </TableBody>
    </Table>
  )
}
