import type { ButtonHTMLAttributes, HTMLAttributes, PropsWithChildren } from "react";
import { cn } from "../lib/utils";
export function Button({ className, ...props }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      className={cn(
        "inline-flex h-9 items-center justify-center gap-2 rounded-md border border-border bg-background px-3 text-sm font-medium shadow-sm transition hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}
export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "rounded-xl border border-border bg-card text-card-foreground shadow-sm",
        className,
      )}
      {...props}
    />
  );
}
export function Badge({ className, ...props }: HTMLAttributes<HTMLSpanElement>) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border border-border bg-muted px-2 py-0.5 text-xs",
        className,
      )}
      {...props}
    />
  );
}
export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("animate-pulse rounded-md bg-muted", className)} />;
}
export function Empty({ children }: PropsWithChildren) {
  return <Card className="p-10 text-center text-muted-foreground">{children}</Card>;
}
export function ErrorState({ retry }: { retry: () => void }) {
  return (
    <Card className="p-8 text-center">
      <p className="mb-4 text-destructive">読み込みに失敗しました。</p>
      <Button onClick={retry}>再試行</Button>
    </Card>
  );
}
