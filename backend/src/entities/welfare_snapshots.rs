use sea_orm::entity::prelude::*;
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "welfare_snapshots")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i64,
    #[sea_orm(column_type = "Decimal(Some((10, 6)))")]
    pub gini: Decimal,
    #[sea_orm(column_type = "Decimal(Some((22, 4)))")]
    pub total_equity: Decimal,
    #[sea_orm(column_type = "Decimal(Some((20, 4)))")]
    pub mean_equity: Decimal,
    pub ts: DateTimeUtc,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {}

impl ActiveModelBehavior for ActiveModel {}
