export function Container({
  className = "",
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={`mx-auto w-full max-w-6xl px-6 md:px-10 ${className}`}>
      {children}
    </div>
  );
}
