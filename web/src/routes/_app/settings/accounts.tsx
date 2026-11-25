import { createFileRoute } from "@tanstack/react-router";
import { Notice } from "@/components/notice";
import { settingsSearch } from "@/components/settings/settings-card";
import { SectionPage } from "@/components/settings/settings-page";
import { added } from "@/lib/accounts";
import { accountsPage } from "@/lib/settings";
import { settingsQuery } from "@/queries/modules";

const mono = "font-mono text-[13px]";

export const Route = createFileRoute("/_app/settings/accounts")({
  validateSearch: settingsSearch,
  loader: ({ context }) => void context.queryClient.prefetchQuery(settingsQuery({ section: accountsPage.section })),
  head: () => ({ meta: [{ title: "Accounts · otogi" }] }),
  component: () => (
    <SectionPage
      page={accountsPage}
      about={
        <>
          The platform accounts the bot runs as. A saved change applies on restart. An account's name cannot be changed
          once saved: its login, chats, and what modules keep for it are under that name. A wrong credential, or a
          Telegram account not logged in, is found only when the bot starts, and then keeps it from starting: the way
          back is <span className={mono}>otogi settings revert system:accounts</span>, run on the server.
        </>
      }
      review={(was, now) => {
        const telegram = added(was, now, "telegram");
        return (
          telegram.length > 0 && (
            <Notice tone="warning" title="Log in before restarting">
              <p className="mt-0.5">
                {telegram.join(", ")}{" "}
                {telegram.length === 1 ? "is a new Telegram account" : "are new Telegram accounts"}. On the server, run{" "}
                {telegram.map((name, i) => (
                  <span key={name}>
                    {i > 0 && ", "}
                    <span className={mono}>otogi login {name}</span>
                  </span>
                ))}{" "}
                before restarting: a Telegram account that is not logged in keeps the bot, and this panel with it, from
                starting. The way back is then <span className={mono}>otogi settings revert system:accounts</span>.
              </p>
            </Notice>
          )
        );
      }}
    />
  ),
});
