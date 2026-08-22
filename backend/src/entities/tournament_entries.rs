use sea_orm::entity::prelude::*;
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "tournament_entries")]
pub struct Model {
    #[sea_orm(primary_key, auto_increment = false)]
    pub tournament_id: Uuid,
    #[sea_orm(primary_key, auto_increment = false)]
    pub agent_id: Uuid,
    pub strategy: String,
    #[sea_orm(column_type = "Decimal(Some((20, 4)))")]
    pub start_equity: Decimal,
    pub total_volume: i64,
    pub prosocial_volume: i64,
    #[sea_orm(column_type = "Decimal(Some((14, 6)))", nullable)]
    pub return_pct: Option<Decimal>,
    #[sea_orm(column_type = "Decimal(Some((10, 6)))", nullable)]
    pub coop_share: Option<Decimal>,
    #[sea_orm(column_type = "Decimal(Some((16, 6)))", nullable)]
    pub score: Option<Decimal>,
    pub finished_at: Option<DateTimeUtc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {}

impl ActiveModelBehavior for ActiveModel {}
