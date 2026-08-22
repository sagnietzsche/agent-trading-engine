use sea_orm_migration::{prelude::*, schema::*};

#[derive(DeriveMigrationName)]
pub struct Migration;

#[async_trait::async_trait]
impl MigrationTrait for Migration {
    async fn up(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager
            .create_table(
                Table::create()
                    .table(Agents::Table)
                    .if_not_exists()
                    .col(uuid(Agents::Id).primary_key())
                    .col(string(Agents::Name).not_null())
                    .col(boolean(Agents::IsBot).not_null().default(false))
                    .col(decimal_len(Agents::Cash, 20, 4).not_null().default(0))
                    .col(decimal_len(Agents::ReservedCash, 20, 4).not_null().default(0))
                    .col(timestamp_with_time_zone(Agents::CreatedAt).not_null().default(Expr::current_timestamp()))
                    .to_owned(),
            )
            .await?;

        manager
            .create_table(
                Table::create()
                    .table(Stocks::Table)
                    .if_not_exists()
                    .col(string(Stocks::Symbol).primary_key())
                    .col(string(Stocks::Name).not_null())
                    .col(decimal_len(Stocks::Fair, 20, 4).not_null())
                    .col(decimal_len(Stocks::PrevClose, 20, 4).not_null())
                    .to_owned(),
            )
            .await?;

        manager
            .create_table(
                Table::create()
                    .table(Orders::Table)
                    .if_not_exists()
                    .col(big_integer(Orders::Id).primary_key())
                    .col(uuid(Orders::AgentId).not_null())
                    .col(string(Orders::Symbol).not_null())
                    .col(string(Orders::Side).not_null())
                    .col(string(Orders::Kind).not_null())
                    .col(decimal_len(Orders::Price, 20, 4).null())
                    .col(integer(Orders::Qty).not_null())
                    .col(integer(Orders::Filled).not_null().default(0))
                    .col(string(Orders::Status).not_null())
                    .col(timestamp_with_time_zone(Orders::CreatedAt).not_null().default(Expr::current_timestamp()))
                    .foreign_key(
                        ForeignKey::create()
                            .name("fk_orders_agent")
                            .from(Orders::Table, Orders::AgentId)
                            .to(Agents::Table, Agents::Id),
                    )
                    .foreign_key(
                        ForeignKey::create()
                            .name("fk_orders_stock")
                            .from(Orders::Table, Orders::Symbol)
                            .to(Stocks::Table, Stocks::Symbol),
                    )
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(Index::create().name("idx_orders_agent").table(Orders::Table).col(Orders::AgentId).to_owned())
            .await?;
        manager
            .create_index(Index::create().name("idx_orders_status").table(Orders::Table).col(Orders::Status).to_owned())
            .await?;

        manager
            .create_table(
                Table::create()
                    .table(Trades::Table)
                    .if_not_exists()
                    .col(uuid(Trades::Id).primary_key())
                    .col(string(Trades::Symbol).not_null())
                    .col(decimal_len(Trades::Price, 20, 4).not_null())
                    .col(integer(Trades::Qty).not_null())
                    .col(uuid(Trades::Buyer).not_null())
                    .col(uuid(Trades::Seller).not_null())
                    .col(big_integer(Trades::TakerOrder).not_null())
                    .col(decimal_len(Trades::BuyerEquity, 20, 4).not_null().default(0))
                    .col(decimal_len(Trades::SellerEquity, 20, 4).not_null().default(0))
                    .col(decimal_len(Trades::GiniAfter, 10, 6).not_null().default(0))
                    .col(timestamp_with_time_zone(Trades::Ts).not_null().default(Expr::current_timestamp()))
                    .foreign_key(
                        ForeignKey::create()
                            .name("fk_trades_stock")
                            .from(Trades::Table, Trades::Symbol)
                            .to(Stocks::Table, Stocks::Symbol),
                    )
                    .foreign_key(
                        ForeignKey::create()
                            .name("fk_trades_taker_order")
                            .from(Trades::Table, Trades::TakerOrder)
                            .to(Orders::Table, Orders::Id),
                    )
                    .to_owned(),
            )
            .await?;

        manager
            .create_index(Index::create().name("idx_trades_symbol_ts").table(Trades::Table).col(Trades::Symbol).col(Trades::Ts).to_owned())
            .await?;

        manager
            .create_table(
                Table::create()
                    .table(Positions::Table)
                    .if_not_exists()
                    .col(uuid(Positions::AgentId).not_null())
                    .col(string(Positions::Symbol).not_null())
                    .col(integer(Positions::Qty).not_null().default(0))
                    .primary_key(
                        Index::create()
                            .name("pk_positions")
                            .col(Positions::AgentId)
                            .col(Positions::Symbol),
                    )
                    .foreign_key(
                        ForeignKey::create()
                            .name("fk_positions_agent")
                            .from(Positions::Table, Positions::AgentId)
                            .to(Agents::Table, Agents::Id),
                    )
                    .foreign_key(
                        ForeignKey::create()
                            .name("fk_positions_stock")
                            .from(Positions::Table, Positions::Symbol)
                            .to(Stocks::Table, Stocks::Symbol),
                    )
                    .to_owned(),
            )
            .await?;

        manager
            .create_table(
                Table::create()
                    .table(WelfareSnapshots::Table)
                    .if_not_exists()
                    .col(big_integer(WelfareSnapshots::Id).auto_increment().primary_key())
                    .col(decimal_len(WelfareSnapshots::Gini, 10, 6).not_null())
                    .col(decimal_len(WelfareSnapshots::TotalEquity, 22, 4).not_null())
                    .col(decimal_len(WelfareSnapshots::MeanEquity, 20, 4).not_null())
                    .col(timestamp_with_time_zone(WelfareSnapshots::Ts).not_null().default(Expr::current_timestamp()))
                    .to_owned(),
            )
            .await?;

        Ok(())
    }

    async fn down(&self, manager: &SchemaManager) -> Result<(), DbErr> {
        manager.drop_table(Table::drop().table(WelfareSnapshots::Table).to_owned()).await?;
        manager.drop_table(Table::drop().table(Positions::Table).to_owned()).await?;
        manager.drop_table(Table::drop().table(Trades::Table).to_owned()).await?;
        manager.drop_table(Table::drop().table(Orders::Table).to_owned()).await?;
        manager.drop_table(Table::drop().table(Stocks::Table).to_owned()).await?;
        manager.drop_table(Table::drop().table(Agents::Table).to_owned()).await?;
        Ok(())
    }
}

#[derive(DeriveIden)]
enum Agents {
    Table,
    Id,
    Name,
    IsBot,
    Cash,
    ReservedCash,
    CreatedAt,
}

#[derive(DeriveIden)]
enum Stocks {
    Table,
    Symbol,
    Name,
    Fair,
    PrevClose,
}

#[derive(DeriveIden)]
enum Orders {
    Table,
    Id,
    AgentId,
    Symbol,
    Side,
    Kind,
    Price,
    Qty,
    Filled,
    Status,
    CreatedAt,
}

#[derive(DeriveIden)]
enum Trades {
    Table,
    Id,
    Symbol,
    Price,
    Qty,
    Buyer,
    Seller,
    TakerOrder,
    BuyerEquity,
    SellerEquity,
    GiniAfter,
    Ts,
}

#[derive(DeriveIden)]
enum Positions {
    Table,
    AgentId,
    Symbol,
    Qty,
}

#[derive(DeriveIden)]
enum WelfareSnapshots {
    Table,
    Id,
    Gini,
    TotalEquity,
    MeanEquity,
    Ts,
}
