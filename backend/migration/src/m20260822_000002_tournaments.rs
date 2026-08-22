use sea_orm_migration::{prelude::*, schema::*};

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .create_table(
                Table::create()
                    .table(Tournaments::Table)
                    .if_not_exists()
                    .col(uuid(Tournaments::Id).primary_key())
                    .col(string(Tournaments::Name).not_null())
                    .col(string(Tournaments::Status).not_null().default("open"))
                    .col(integer(Tournaments::DurationTicks).not_null().default(90))
                    .col(integer(Tournaments::TicksLeft).not_null().default(90))
                    .col(decimal_len(Tournaments::GiniStart, 10, 6).not_null().default(0))
                    .col(decimal_len(Tournaments::GiniFinal, 10, 6).null())
                    .col(timestamp_with_time_zone(Tournaments::CreatedAt).not_null().default(Expr::current_timestamp()))
                    .col(timestamp_with_time_zone(Tournaments::StartedAt).null())
                    .col(timestamp_with_time_zone(Tournaments::FinishedAt).null())
                    .to_owned(),
            )
            .await?;

        manager
            .create_table(
                Table::create()
                    .table(TournamentEntries::Table)
                    .if_not_exists()
                    .col(uuid(TournamentEntries::TournamentId).not_null())
                    .col(uuid(TournamentEntries::AgentId).not_null())
                    .col(string(TournamentEntries::Strategy).not_null().default("custom"))
                    .col(decimal_len(TournamentEntries::StartEquity, 20, 4).not_null().default(0))
                    .col(big_integer(TournamentEntries::TotalVolume).not_null().default(0))
                    .col(big_integer(TournamentEntries::ProsocialVolume).not_null().default(0))
                    .col(decimal_len(TournamentEntries::ReturnPct, 14, 6).null())
                    .col(decimal_len(TournamentEntries::CoopShare, 10, 6).null())
                    .col(decimal_len(TournamentEntries::Score, 16, 6).null())
                    .col(timestamp_with_time_zone(TournamentEntries::FinishedAt).null())
                    .primary_key(
                        Index::create()
                            .name("pk_tournament_entries")
                            .col(TournamentEntries::TournamentId)
                            .col(TournamentEntries::AgentId),
                    )
                    .foreign_key(
                        ForeignKey::create()
                            .name("fk_tentry_tournament")
                            .from(TournamentEntries::Table, TournamentEntries::TournamentId)
                            .to(Tournaments::Table, Tournaments::Id)
                            .on_delete(ForeignKeyAction::Cascade),
                    )
                    .foreign_key(
                        ForeignKey::create()
                            .name("fk_tentry_agent")
                            .from(TournamentEntries::Table, TournamentEntries::AgentId)
                            .to(Agents::Table, Agents::Id),
                    )
                    .to_owned(),
            )
            .await?;

        Ok(())
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .drop_table(Table::drop().table(TournamentEntries::Table).to_owned())
            .await?;
        manager
            .drop_table(Table::drop().table(Tournaments::Table).to_owned())
            .await?;
        Ok(())
    }
}

#[derive(DeriveIden)]
enum Tournaments {
    Table,
    Id,
    Name,
    Status,
    DurationTicks,
    TicksLeft,
    GiniStart,
    GiniFinal,
    CreatedAt,
    StartedAt,
    FinishedAt,
}

#[derive(DeriveIden)]
enum TournamentEntries {
    Table,
    TournamentId,
    AgentId,
    Strategy,
    StartEquity,
    TotalVolume,
    ProsocialVolume,
    ReturnPct,
    CoopShare,
    Score,
    FinishedAt,
}

#[derive(DeriveIden)]
enum Agents {
    Table,
    Id,
}
